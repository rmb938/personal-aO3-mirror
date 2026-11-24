package auth

import (
	"errors"
	"sync/atomic"
)

// LiveKeyring is the Keyring in use, which a master key rotation swaps without a restart
// (docs/part3-key-rotation.md "The keyring"). Load returns a consistent snapshot, not
// necessarily the newest one; that is safe because a retired key stays in every snapshot for
// days after a rotation, far longer than any request holds one.
type LiveKeyring struct{ p atomic.Pointer[Keyring] }

// NewLiveKeyring returns a LiveKeyring holding r.
func NewLiveKeyring(r *Keyring) (*LiveKeyring, error) {
	if r == nil {
		return nil, errors.New("auth: nil keyring")
	}
	l := &LiveKeyring{}
	l.p.Store(r)
	return l, nil
}

// Load returns the current Keyring. Load once per operation and use that snapshot throughout.
func (l *LiveKeyring) Load() *Keyring { return l.p.Load() }

// Store swaps in r (a master key rotation's new keyring). Loads already taken keep their
// snapshot. r must not be nil.
func (l *LiveKeyring) Store(r *Keyring) {
	if r == nil {
		panic("auth: nil keyring")
	}
	l.p.Store(r)
}
