package auth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// live returns a LiveKeyring holding r.
func live(t *testing.T, r *Keyring) *LiveKeyring {
	t.Helper()
	l, err := NewLiveKeyring(r)
	require.NoError(t, err)
	return l
}

func TestLiveKeyring(t *testing.T) {
	_, err := NewLiveKeyring(nil)
	require.Error(t, err)

	r := newTestKeyring(t)
	l := live(t, r)
	require.Same(t, r, l.Load())

	// A snapshot loaded before a swap keeps its keyring; new loads get the new one.
	snap := l.Load()
	r2 := newTestKeyring(t)
	l.Store(r2)
	require.Same(t, r, snap)
	require.Same(t, r2, l.Load())
	require.Panics(t, func() { l.Store(nil) })
}
