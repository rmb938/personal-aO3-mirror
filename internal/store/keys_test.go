package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openKeysTest(t *testing.T) (*Keys, *KeysTx) {
	t.Helper()
	k, err := OpenKeys(context.Background(), filepath.Join(t.TempDir(), "api"), Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, k.Close()) })
	return k, k.AutoCommitForTestOnly()
}

func TestOpenKeysFileIsPrivate(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "api")
	k, err := OpenKeys(ctx, dir, Options{})
	require.NoError(t, err)
	fi, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), fi.Mode().Perm())
	fi, err = os.Stat(filepath.Join(dir, KeysFile))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	// Rollback-journal mode: no -wal or -shm, and the journal is gone between transactions.
	for _, f := range []string{KeysFile + "-wal", KeysFile + "-shm", KeysFile + "-journal"} {
		_, err := os.Stat(filepath.Join(dir, f))
		require.ErrorIs(t, err, os.ErrNotExist, f)
	}
	require.NoError(t, k.Close())

	k, err = OpenKeys(ctx, dir, Options{})
	require.NoError(t, err)
	require.NoError(t, k.Close())
	require.NoError(t, os.Chmod(filepath.Join(dir, KeysFile), 0o644)) //nolint:gosec // G302: the test needs a world-readable db
	_, err = OpenKeys(ctx, dir, Options{})
	require.ErrorContains(t, err, "chmod 600")
}

// Every connection runs with journal_mode=DELETE, synchronous=FULL and secure_delete=ON.
func TestOpenKeysPragmas(t *testing.T) {
	k, _ := openKeysTest(t)
	ctx := context.Background()
	// More connections than one, so the pragmas are checked per connection, not just once.
	conns := make([]interface{ Close() error }, 0, 3)
	for range 3 {
		c, err := k.db.Conn(ctx)
		require.NoError(t, err)
		conns = append(conns, c)
		var journal string
		var syncMode, secureDelete int
		require.NoError(t, c.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journal))
		require.NoError(t, c.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&syncMode))
		require.NoError(t, c.QueryRowContext(ctx, `PRAGMA secure_delete`).Scan(&secureDelete))
		assert.Equal(t, "delete", journal)
		assert.Equal(t, 2, syncMode, "FULL")
		assert.Equal(t, 1, secureDelete)
	}
	for _, c := range conns {
		require.NoError(t, c.Close())
	}
}

// A deleted key's bytes are overwritten in the file, not left in a free page.
func TestKeysSecureDelete(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "api")
	k, err := OpenKeys(ctx, dir, Options{})
	require.NoError(t, err)
	key := bytes.Repeat([]byte{0xA5, 0x5A, 0xC3, 0x3C}, 8)
	_, err = k.AutoCommitForTestOnly().InsertMasterKey(ctx, key, usersT0)
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(dir, KeysFile))
	require.NoError(t, err)
	require.True(t, bytes.Contains(raw, key), "the key is in the file")

	_, err = k.db.ExecContext(ctx, `DELETE FROM master_keys`)
	require.NoError(t, err)
	require.NoError(t, k.Close())
	raw, err = os.ReadFile(filepath.Join(dir, KeysFile))
	require.NoError(t, err)
	require.False(t, bytes.Contains(raw, key), "secure_delete zeroed it")
}

func TestMasterKeys(t *testing.T) {
	k, tx := openKeysTest(t)
	ctx := context.Background()
	got, err := tx.MasterKeys(ctx)
	require.NoError(t, err)
	require.Empty(t, got)

	key := bytes.Repeat([]byte{1}, 32)
	id, err := tx.InsertMasterKey(ctx, key, usersT0)
	require.NoError(t, err)
	require.EqualValues(t, 1, id)

	// One active key at most, and keys are 32 bytes.
	_, err = tx.InsertMasterKey(ctx, bytes.Repeat([]byte{2}, 32), usersT0)
	require.Error(t, err, "a second active key")
	_, err = k.db.ExecContext(ctx, `UPDATE master_keys SET retired_at = ? WHERE id = 1`, usersT0.Unix()+1)
	require.NoError(t, err)
	_, err = tx.InsertMasterKey(ctx, bytes.Repeat([]byte{2}, 31), usersT0)
	require.Error(t, err, "a short key")
	id2, err := tx.InsertMasterKey(ctx, bytes.Repeat([]byte{2}, 32), usersT0)
	require.NoError(t, err)
	require.EqualValues(t, 2, id2)

	// AUTOINCREMENT: a deleted key's ID is never reused.
	_, err = k.db.ExecContext(ctx, `DELETE FROM master_keys WHERE id = 2`)
	require.NoError(t, err)
	id2, err = tx.InsertMasterKey(ctx, bytes.Repeat([]byte{2}, 32), usersT0)
	require.NoError(t, err)
	require.EqualValues(t, 3, id2)

	got, err = tx.MasterKeys(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.EqualValues(t, 1, got[0].ID)
	assert.Equal(t, key, got[0].Key)
	assert.True(t, got[0].CreatedAt.Equal(usersT0))
	assert.True(t, got[0].RetiredAt.Equal(usersT0.Add(1e9)))
	assert.Equal(t, id2, got[1].ID)
	assert.True(t, got[1].RetiredAt.IsZero(), "active")
}

func TestKeysTxGuard(t *testing.T) {
	k, _ := openKeysTest(t)
	u, _ := openUsersTest(t)
	ctx := context.Background()
	err := k.InTx(ctx, func(ctx context.Context, _ *KeysTx) error {
		return k.InReadTx(ctx, func(context.Context, *KeysReadTx) error { return nil })
	})
	require.ErrorIs(t, err, ErrTxMisuse)
	// Neither keys.db nor users.db opens inside the other's transaction.
	err = u.InReadTx(ctx, func(ctx context.Context, _ *UsersReadTx) error {
		return k.InReadTx(ctx, func(context.Context, *KeysReadTx) error { return nil })
	})
	require.ErrorIs(t, err, ErrTxMisuse)
	err = k.InReadTx(ctx, func(ctx context.Context, _ *KeysReadTx) error {
		return u.InTx(ctx, func(context.Context, *UsersTx) error { return nil })
	})
	require.ErrorIs(t, err, ErrTxMisuse)

	// A failing write rolls back.
	err = k.InTx(ctx, func(ctx context.Context, tx *KeysTx) error {
		if _, err := tx.InsertMasterKey(ctx, bytes.Repeat([]byte{1}, 32), usersT0); err != nil {
			return err
		}
		return os.ErrInvalid
	})
	require.ErrorIs(t, err, os.ErrInvalid)
	got, err := ReadKeysValue(ctx, k, func(ctx context.Context, tx *KeysReadTx) (int, error) {
		ks, err := tx.MasterKeys(ctx)
		return len(ks), err
	})
	require.NoError(t, err)
	require.Zero(t, got)
}
