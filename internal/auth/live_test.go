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
	require.Same(t, r, live(t, r).Load())
}
