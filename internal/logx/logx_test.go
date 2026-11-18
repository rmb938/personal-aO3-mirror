package logx

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFanoutWritesBoth(t *testing.T) {
	dir := t.TempDir()
	var stderr bytes.Buffer
	l, c, err := New(Options{Level: "info", File: filepath.Join(dir, "x.log"), MaxBytes: 1 << 20, MaxFiles: 2, Stderr: &stderr})
	require.NoError(t, err)
	l.Info("hello", "work_id", 42)
	l.Debug("hidden")
	require.NoError(t, c.Close())
	raw, err := os.ReadFile(filepath.Join(dir, "x.log"))
	require.NoError(t, err)
	require.Contains(t, string(raw), `"work_id":42`)
	require.Contains(t, stderr.String(), "work_id=42")
	require.NotContains(t, stderr.String(), "hidden", "debug leaked at info level")
}

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "r.log")
	r, err := OpenRotating(p, 10, 2)
	require.NoError(t, err)
	for range 5 {
		_, err := r.Write([]byte("0123456789"))
		require.NoError(t, err)
	}
	require.NoError(t, r.Close())
	for _, name := range []string{"r.log", "r.log.1", "r.log.2"} {
		assert.FileExists(t, filepath.Join(dir, name))
	}
	assert.NoFileExists(t, filepath.Join(dir, "r.log.3"), "kept too many files")
}
