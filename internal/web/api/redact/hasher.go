// Package redact is the API's fanwork-protection core: a keyed hasher and a response enforcer
// that replaces every JSON string not provably safe with a hash (docs/part2-api.md, "Fanwork
// protection"). It has no HTTP dependencies, so the API middleware is a thin wrapper around it.
package redact

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// MinRotation is the shortest allowed key rotation interval (0 means once per process).
const MinRotation = time.Hour

const (
	keyBytes     = 32
	epochIDBytes = 8
	// hashHexLen is the number of hex characters of the HMAC kept after the "h_" prefix.
	hashHexLen = 24
	hashPrefix = "h_"
)

// Epoch is one hash key with its random public ID. It is immutable. The key never leaves the
// package: String, GoString and LogValue print only the ID.
type Epoch struct {
	id  string
	key []byte
}

// ID is the epoch's public random ID, sent as the X-Fanwork-Key-Epoch header.
func (e *Epoch) ID() string { return e.id }

// Hash returns "h_" + 24 hex of HMAC-SHA256(key, s).
func (e *Epoch) Hash(s string) string { return e.newMac().hash(s) }

// The printing methods have value receivers so a copied Epoch can't print its key either.

func (e Epoch) String() string             { return "epoch(" + e.id + ")" }
func (e Epoch) GoString() string           { return e.String() }
func (e Epoch) LogValue() slog.Value       { return slog.StringValue(e.id) }
func (e Epoch) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(e.String())) }

func (e *Epoch) newMac() *mac { return &mac{h: hmac.New(sha256.New, e.key)} }

// mac is a reusable HMAC for hashing many strings under one epoch (one response).
type mac struct {
	h   hash.Hash
	sum []byte
}

func (m *mac) hash(s string) string {
	m.h.Reset()
	_, _ = m.h.Write([]byte(s)) // hash.Hash.Write never fails
	m.sum = m.h.Sum(m.sum[:0])
	var buf [len(hashPrefix) + hashHexLen]byte
	copy(buf[:], hashPrefix)
	hex.Encode(buf[len(hashPrefix):], m.sum[:hashHexLen/2])
	return string(buf[:])
}

func newEpoch() *Epoch {
	key := make([]byte, keyBytes)
	id := make([]byte, epochIDBytes)
	// crypto/rand.Read never returns an error (it crashes the program instead).
	_, _ = rand.Read(key)
	_, _ = rand.Read(id)
	return &Epoch{id: hex.EncodeToString(id), key: key}
}

type epochState struct {
	epoch   *Epoch
	expires time.Time // zero: never
}

// Hasher holds the current Epoch and rotates it every rotation interval, driven by now. Keys
// are made with crypto/rand, held only in memory and never stored or logged. It is safe for
// concurrent use.
type Hasher struct {
	rotation time.Duration
	now      func() time.Time
	mu       sync.Mutex // serializes rotation
	cur      atomic.Pointer[epochState]
}

// NewHasher makes a Hasher with a fresh key. rotation 0 keeps one key for the process;
// otherwise it must be at least MinRotation. now is the clock (nil: time.Now).
func NewHasher(rotation time.Duration, now func() time.Time) (*Hasher, error) {
	if rotation < 0 || (rotation > 0 && rotation < MinRotation) {
		return nil, fmt.Errorf("redact: hash key rotation %s: must be 0 or at least %s", rotation, MinRotation)
	}
	if now == nil {
		now = time.Now
	}
	h := &Hasher{rotation: rotation, now: now}
	h.cur.Store(h.next(now()))
	return h, nil
}

func (h *Hasher) next(at time.Time) *epochState {
	st := &epochState{epoch: newEpoch()}
	if h.rotation > 0 {
		st.expires = at.Add(h.rotation)
	}
	return st
}

// Current returns the epoch to use, rotating first if it expired. Callers take one snapshot
// per response so the whole response is hashed with one key.
func (h *Hasher) Current() *Epoch {
	st := h.cur.Load()
	if st.expires.IsZero() {
		return st.epoch
	}
	now := h.now()
	if now.Before(st.expires) {
		return st.epoch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if st = h.cur.Load(); !now.Before(st.expires) {
		st = h.next(now)
		h.cur.Store(st)
	}
	return st.epoch
}
