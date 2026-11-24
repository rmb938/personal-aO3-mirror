package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// TestBackupCLI backs up to a relative path (made absolute by the CLI, not by serve), then
// refuses the same directory again.
func TestBackupCLI(t *testing.T) {
	c := newCLI(t, "")
	c.serve(false)
	cwd := t.TempDir()
	t.Chdir(cwd)

	code, out, errOut := c.run("backup", "b1")
	require.Equal(t, 0, code, "backup: %s", errOut)
	dir := filepath.Join(cwd, "b1")
	assert.Contains(t, out, "backup written to "+dir)
	for _, name := range []string{store.UsersFile, store.KeysFile} {
		_, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err, name)
	}

	code, _, errOut = c.run("backup", dir)
	assert.Equal(t, 1, code, "backup into an existing directory")
	assert.Contains(t, errOut, "already exists")

	for _, args := range [][]string{{"backup", "a", "b"}, {"backup", ""}} {
		code, _, errOut := c.run(args...)
		assert.Equal(t, 2, code, "%q is a usage error", args)
		assert.Contains(t, errOut, "usage: ao3mirror backup DIR")
	}
	code, out, _ = c.run("backup")
	assert.Equal(t, 0, code, "bare backup prints help")
	assert.Contains(t, out, "To restore")
}
