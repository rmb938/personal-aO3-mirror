package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// TestBackupCLI backs up under api.backup_dir, then refuses the same name again and a name that
// is a path.
func TestBackupCLI(t *testing.T) {
	c := newCLI(t, "")
	c.serve(false)
	cfg := c.config()

	code, out, errOut := c.run("backup", "b1")
	require.Equal(t, 0, code, "backup: %s", errOut)
	dir := cfg.Path(cfg.API.BackupDir, "b1")
	assert.Contains(t, out, "backup written to "+dir)
	for _, name := range []string{store.UsersFile, store.KeysFile} {
		_, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err, name)
	}

	code, _, errOut = c.run("backup", "b1")
	assert.Equal(t, 1, code, "backup into an existing name")
	assert.Contains(t, errOut, "already exists")
	code, _, _ = c.run("backup", "../b2")
	assert.Equal(t, 1, code, "a name that is a path")
	_, err := os.Stat(cfg.Path("b2"))
	require.ErrorIs(t, err, os.ErrNotExist)

	for _, args := range [][]string{{"backup", "a", "b"}, {"backup", ""}} {
		code, _, errOut := c.run(args...)
		assert.Equal(t, 2, code, "%q is a usage error", args)
		assert.Contains(t, errOut, "usage: ao3mirror backup NAME")
	}
	code, out, _ = c.run("backup")
	assert.Equal(t, 0, code, "bare backup prints help")
	assert.Contains(t, out, "To restore")
}
