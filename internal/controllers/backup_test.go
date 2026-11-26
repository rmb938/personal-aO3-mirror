package controllers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// TestBackupRestores backs up mid-retention (two keys held), then opens the copies as a fresh
// serve would: the startup check passes and every value decrypts.
func TestBackupRestores(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	seedSecrets(t, e)
	_, err := e.mk.RotateMasterKey(ctx)
	require.NoError(t, err)
	e.signup(t, "fixture_after") // a second account, after the rotation

	require.NoError(t, e.mk.BackupDatabases(ctx, "backup"))
	fi, err := os.Stat(e.mk.backupDir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), fi.Mode().Perm(), "the backup directory is created private")
	dir := filepath.Join(e.mk.backupDir, "backup")
	fi, err = os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), fi.Mode().Perm())
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, en := range entries {
		names = append(names, en.Name())
		fi, err := en.Info()
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), en.Name())
	}
	assert.ElementsMatch(t, []string{store.UsersFile, store.KeysFile}, names)
	assert.Contains(t, e.logs.String(), `"event":"backup_created"`)
	assert.NotContains(t, e.logs.String(), e.mk.backupDir, "the path is not logged")

	// Restore: copy both files into a new api dir and start on them.
	api := filepath.Join(t.TempDir(), "api")
	require.NoError(t, os.Mkdir(api, 0o700))
	for _, name := range []string{store.UsersFile, store.KeysFile} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(api, name), b, 0o600)) //nolint:gosec // G703: a temp dir and constant file names

	}
	users, err := store.OpenUsers(ctx, api, store.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, users.Close()) })
	keys, err := store.OpenKeys(ctx, api, store.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, keys.Close()) })
	assert.Equal(t, masterKeyIDs(t, e.keys), masterKeyIDs(t, keys), "both keys are in the copy")
	mk, err := NewMasterKeys(ctx, MasterKeysOptions{Keys: keys, Users: users})
	require.NoError(t, err)
	restored := &authEnv{users: users}
	requireDecrypts(t, restored, mk.Keyring().Load())
	n, err := store.ReadUsersValue(ctx, users, func(ctx context.Context, tx *store.UsersReadTx) (int, error) {
		us, err := tx.ListUsers(ctx)
		return len(us), err
	})
	require.NoError(t, err)
	assert.Equal(t, 2, n, "both accounts are in the copy")
}

func TestBackupRefusals(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()

	existing := filepath.Join(e.mk.backupDir, "existing")
	require.NoError(t, os.MkdirAll(existing, 0o700))
	require.ErrorIs(t, e.mk.BackupDatabases(ctx, "existing"), ErrBackupExists)
	entries, err := os.ReadDir(existing)
	require.NoError(t, err)
	assert.Empty(t, entries, "an existing directory is left alone")

	// Anything but one plain component never becomes a path, whatever reached the controller.
	for _, name := range []string{"", ".", "..", "../escape", "a/b", "/abs", ".hidden", `a\b`, "a b", "é",
		strings.Repeat("a", MaxBackupNameBytes+1)} {
		require.ErrorIs(t, e.mk.BackupDatabases(ctx, name), ErrBadBackupName, "%q", name)
	}
	_, err = os.Stat(filepath.Join(filepath.Dir(e.mk.backupDir), "escape"))
	require.ErrorIs(t, err, os.ErrNotExist)

	err = e.users.InReadTx(ctx, func(ctx context.Context, _ *store.UsersReadTx) error {
		return e.mk.BackupDatabases(ctx, "in-tx")
	})
	require.ErrorIs(t, err, store.ErrTxMisuse)
	_, err = os.Stat(filepath.Join(e.mk.backupDir, "in-tx"))
	require.ErrorIs(t, err, os.ErrNotExist)

	// A backup directory others can reach is refused, and nothing is written into it.
	require.NoError(t, os.Chmod(e.mk.backupDir, 0o755)) //nolint:gosec // G302: the test makes it too open on purpose
	require.ErrorContains(t, e.mk.BackupDatabases(ctx, "open"), "private to this user")
	_, err = os.Stat(filepath.Join(e.mk.backupDir, "open"))
	require.ErrorIs(t, err, os.ErrNotExist)

	none, err := NewMasterKeys(ctx, MasterKeysOptions{Keys: e.keys, Users: e.users})
	require.NoError(t, err)
	require.ErrorContains(t, none.BackupDatabases(ctx, "b"), "no backup directory configured")
}

func TestValidBackupName(t *testing.T) {
	for _, n := range []string{"b", "2026-11-26", "nightly_01.bak", "-x", strings.Repeat("a", MaxBackupNameBytes)} {
		assert.True(t, ValidBackupName(n), n)
	}
	for _, n := range []string{"", ".", "..", ".x", "a/b", "a\x00", "a b", "é"} {
		assert.False(t, ValidBackupName(n), "%q", n)
	}
}

// TestBackupWaitsForRotation: a backup started while a rotation holds the mutex starts only
// once the rotation is done.
func TestBackupWaitsForRotation(t *testing.T) {
	e := newAuthEnv(t)
	dir := filepath.Join(e.mk.backupDir, "backup")
	e.mk.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- e.mk.BackupDatabases(context.Background(), "backup") }()
	time.Sleep(50 * time.Millisecond)
	_, err := os.Stat(dir)
	require.ErrorIs(t, err, os.ErrNotExist, "nothing is written while the mutex is held")
	e.mk.mu.Unlock()
	require.NoError(t, <-done)
	_, err = os.Stat(filepath.Join(dir, store.KeysFile))
	require.NoError(t, err)
}
