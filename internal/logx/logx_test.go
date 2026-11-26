package logx

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/dirs/dirstest"
)

func TestFanoutWritesBoth(t *testing.T) {
	dir := t.TempDir()
	var stderr bytes.Buffer
	l, c, err := New(Options{Level: "info", Dir: dirstest.At(t, dir, 0o750), File: "logs/x.log", MaxBytes: 1 << 20,
		MaxFiles: 2, Stderr: &stderr})
	require.NoError(t, err)
	l.Info("hello", "work_id", 42)
	l.Debug("hidden")
	require.NoError(t, c.Close())
	raw, err := os.ReadFile(filepath.Join(dir, "logs", "x.log"))
	require.NoError(t, err)
	require.Contains(t, string(raw), `"work_id":42`)
	require.Contains(t, stderr.String(), "work_id=42")
	require.NotContains(t, stderr.String(), "hidden", "debug leaked at info level")
}

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	r, err := OpenRotating(dirstest.At(t, dir, 0o750), "r.log", 10, 2)
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

// The log file stays inside its directory: a name leading out of it is refused.
func TestLogFileStaysInDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	for _, name := range []string{"../escape.log", "/tmp/abs.log"} {
		_, _, err := New(Options{Dir: dirstest.At(t, dir, 0o750), File: name, Stderr: &bytes.Buffer{}})
		require.Error(t, err, name)
	}
	assert.NoFileExists(t, filepath.Join(filepath.Dir(dir), "escape.log"))
	_, _, err := New(Options{File: "x.log", Stderr: &bytes.Buffer{}})
	require.ErrorContains(t, err, "needs its directory")
}
