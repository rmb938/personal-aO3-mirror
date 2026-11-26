// Package dirstest opens test directories as os.Root values (tests only).
package dirstest

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// Temp is a new temporary directory opened as a root, closed when the test ends.
func Temp(tb testing.TB) *os.Root {
	tb.Helper()
	return At(tb, tb.TempDir(), 0o750)
}

// At creates path (and its parents) with perm when missing and opens it as a root, closed when
// the test ends.
func At(tb testing.TB, path string, perm os.FileMode) *os.Root {
	tb.Helper()
	require.NoError(tb, os.MkdirAll(path, perm))
	r, err := os.OpenRoot(path)
	require.NoError(tb, err)
	tb.Cleanup(func() { require.NoError(tb, r.Close()) })
	return r
}
