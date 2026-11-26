package dirs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenDataNeverCreates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	_, err := OpenData(path)
	require.ErrorIs(t, err, ErrNoDataDir)
	assert.Contains(t, err.Error(), "mkdir -m 750 "+path)
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)

	require.NoError(t, os.Mkdir(path, 0o750))
	r, err := OpenData(path)
	require.NoError(t, err)
	require.NoError(t, r.Close())
}

func TestSubAndPrivateSub(t *testing.T) {
	base := t.TempDir()
	r, err := os.OpenRoot(base)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	s, err := Sub(r, "a/b", 0o750)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(base, "a", "b"), s.Name())
	require.NoError(t, s.Close())

	p, err := PrivateSub(r, "api")
	require.NoError(t, err)
	fi, err := os.Stat(filepath.Join(base, "api"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), fi.Mode().Perm())
	require.NoError(t, p.Close())

	require.NoError(t, os.Chmod(filepath.Join(base, "api"), 0o750)) //nolint:gosec // deliberately unsafe mode under test
	_, err = PrivateSub(r, "api")
	require.ErrorContains(t, err, "chmod 700")

	// Nothing leads outside the parent: not "..", not an absolute name, not a symlink.
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(base, "link")))
	for _, name := range []string{"../escape", "/abs/x", "link/x"} {
		_, err := Sub(r, name, 0o750)
		require.Error(t, err, name)
	}
	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	assert.Empty(t, entries, "nothing was created through the symlink")
}
