package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVacuumInto(t *testing.T) {
	ctx := context.Background()
	k, tx := openKeysTest(t)
	kept := bytes.Repeat([]byte{0x5a}, 32)
	gone := bytes.Repeat([]byte{0xa5}, 32)
	id, err := tx.InsertMasterKey(ctx, gone, time.Unix(1, 0))
	require.NoError(t, err)
	require.NoError(t, tx.RetireMasterKey(ctx, id, time.Unix(2, 0)))
	_, err = tx.InsertMasterKey(ctx, kept, time.Unix(2, 0))
	require.NoError(t, err)
	require.NoError(t, tx.DeleteMasterKey(ctx, id))

	out := filepath.Join(t.TempDir(), "backup")
	require.NoError(t, os.Mkdir(out, 0o700))
	path := filepath.Join(out, KeysFile)
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	require.NoError(t, k.VacuumInto(ctx, path))

	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "the pre-created file keeps its mode")
	entries, err := os.ReadDir(out)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no journal or other file is left beside the copy")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.False(t, bytes.Contains(raw, gone), "a deleted key is not carried into the copy")

	// The copy opens as keys.db and holds the kept key.
	c, err := OpenKeys(ctx, out, Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.Close()) })
	got, err := c.AutoCommitForTestOnly().MasterKeys(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, kept, got[0].Key)

	// A non-empty target is refused; so is a call inside a transaction.
	require.ErrorContains(t, k.VacuumInto(ctx, path), "vacuum into "+KeysFile)
	err = k.InReadTx(ctx, func(ctx context.Context, _ *KeysReadTx) error {
		return k.VacuumInto(ctx, filepath.Join(out, "other.db"))
	})
	require.ErrorIs(t, err, ErrTxMisuse)
}

func TestUsersVacuumInto(t *testing.T) {
	ctx := context.Background()
	u, _ := openUsersTest(t)
	out := t.TempDir()
	path := filepath.Join(out, UsersFile)
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	require.NoError(t, u.VacuumInto(ctx, path))
	c, err := OpenUsers(ctx, out, Options{})
	require.NoError(t, err)
	require.NoError(t, c.Close())
}
