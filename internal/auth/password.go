package auth

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/text/unicode/norm"
)

// Password length bounds in runes after NFC. 8 is NIST SP 800-63B-4's minimum when the
// password is used with a second factor (TOTP is mandatory here).
const (
	MinPasswordRunes = 8
	MaxPasswordRunes = 128
)

// Password policy errors, from CheckPassword. Their messages are fixed text, safe to log.
var (
	ErrPasswordInvalid    = errors.New("auth: password has invalid UTF-8 or a control character")
	ErrPasswordTooShort   = errors.New("auth: password is too short")
	ErrPasswordTooLong    = errors.New("auth: password is too long")
	ErrPasswordIsUsername = errors.New("auth: password equals the username")
	ErrPasswordCommon     = errors.New("auth: password is in the common-password list")
	ErrHasherBusy         = errors.New("auth: password hasher busy")
	ErrMalformedHash      = errors.New("auth: malformed password hash")
)

// CheckPassword applies the password policy (NIST SP 800-63B-4 §3.1.1.2): valid UTF-8 without
// control characters, MinPasswordRunes to MaxPasswordRunes runes after NFC (NormalizePassword),
// not the username, and not in the common-password list. There are no composition rules. The
// username and list comparisons use foldPassword, so they ignore case and compatibility variants.
// It returns the first rule broken, or nil.
func CheckPassword(password, username string) error {
	if !utf8.ValidString(password) || strings.ContainsFunc(password, unicode.IsControl) {
		return ErrPasswordInvalid
	}
	switch n := utf8.RuneCountInString(NormalizePassword(password)); {
	case n < MinPasswordRunes:
		return ErrPasswordTooShort
	case n > MaxPasswordRunes:
		return ErrPasswordTooLong
	}
	folded := foldPassword(password)
	if username != "" && folded == foldPassword(username) {
		return ErrPasswordIsUsername
	}
	if isCommonPassword(folded) {
		return ErrPasswordCommon
	}
	return nil
}

// NormalizePassword is the form a password is hashed in: Unicode NFC, so the same password typed
// on different keyboards or platforms gives the same bytes (NIST SP 800-63B-4 §3.1.1.2 says
// SHOULD use NFC). Hasher applies it itself; callers need it only for their own comparisons.
func NormalizePassword(password string) string { return norm.NFC.String(password) }

// foldPassword is the key the username and common-list checks compare: NFKC, lower case. It is
// looser than NormalizePassword, so a fullwidth or ligature spelling of a listed password is
// refused too. It is only ever compared, never hashed.
func foldPassword(s string) string { return strings.ToLower(norm.NFKC.String(s)) }

//go:embed common-passwords.txt.gz
var commonPasswordsGz []byte

// commonPasswords is the sorted, lower-case list from common-passwords.txt.gz (see
// common-passwords.md), decompressed on first use.
var commonPasswords = sync.OnceValue(func() []string {
	zr, err := gzip.NewReader(bytes.NewReader(commonPasswordsGz))
	if err != nil {
		panic("auth: embedded common-password list: " + err.Error())
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		panic("auth: embedded common-password list: " + err.Error())
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
})

// isCommonPassword reports whether folded (foldPassword) is in the list.
func isCommonPassword(folded string) bool {
	_, found := slices.BinarySearch(commonPasswords(), folded)
	return found
}

// Argon2Params are Argon2id cost parameters. Memory is in KiB.
type Argon2Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
	SaltLen uint32
	KeyLen  uint32
}

// DefaultArgon2 is RFC 9106 §4's second recommended Argon2id option, for when the first's 2 GiB
// is too much: m=64 MiB, t=3, p=4, with RFC 9106's 16-byte salt and a 32-byte hash. With the
// Hasher's 2 concurrent hashes that is at most 128 MiB.
var DefaultArgon2 = Argon2Params{Memory: 64 * 1024, Time: 3, Threads: 4, SaltLen: 16, KeyLen: 32}

// Bounds on parameters read back from a stored hash, so a corrupt row can't make Verify allocate
// without limit.
const (
	maxArgon2Memory  = 1 << 20 // 1 GiB in KiB
	maxArgon2Time    = 16
	maxArgon2Threads = 16
	minArgon2Bytes   = 8
	maxArgon2Bytes   = 64
)

// HasherOptions configure NewHasher. Zero values take the defaults.
type HasherOptions struct {
	Params      Argon2Params  // DefaultArgon2
	Concurrency int           // hashes at once; 2
	MaxWait     time.Duration // longest wait for a slot before ErrHasherBusy; 5s
}

// Hasher hashes and verifies passwords with Argon2id in PHC string format, over the NFC
// password. The Argon2id output is then encrypted (XChaCha20-Poly1305 under a key from the
// Keyring's active master key, bound to its row by a PasswordContext): NIST SP 800-63B-4
// §3.1.1.2's "additional iteration of a keyed hashing or encryption operation using a secret
// key known only to the verifier", with the key "stored separately from the hashed passwords"
// (in keys.db). So a copy of users.db alone allows no offline guessing. Because the keyed step
// comes after Argon2id, master key rotation re-wraps a hash without the password
// (Keyring.RewrapPasswordHash). The PHC string names the master key ("k=<id>").
//
// At most Concurrency hashes run at once (each holds Params.Memory), so a flood of logins can't
// exhaust memory; a caller that waits longer than MaxWait gets ErrHasherBusy (the API answers
// 503).
type Hasher struct {
	keyring *LiveKeyring // read with Load once per call
	params  Argon2Params
	sem     chan struct{}
	maxWait time.Duration
	dummy   phcHash // the parameters and salt of a hash verified for unknown users
	dummyK  []byte  // its random Argon2id output, wrapped afresh at each VerifyDummy
}

// NewHasher returns a Hasher wrapping hashes under the active master key of keyring's current
// Keyring. It builds the dummy hash without running Argon2id.
func NewHasher(keyring *LiveKeyring, opts HasherOptions) (*Hasher, error) {
	if keyring == nil {
		return nil, errors.New("auth: NewHasher needs the Keyring (to wrap password hashes)")
	}
	if opts.Params == (Argon2Params{}) {
		opts.Params = DefaultArgon2
	}
	if err := opts.Params.check(); err != nil {
		return nil, err
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 2
	}
	if opts.MaxWait <= 0 {
		opts.MaxWait = 5 * time.Second
	}
	h := &Hasher{keyring: keyring, params: opts.Params, sem: make(chan struct{}, opts.Concurrency), maxWait: opts.MaxWait}
	h.dummy = phcHash{params: h.params, salt: randomBytes(int(h.params.SaltLen))}
	h.dummyK = randomBytes(int(h.params.KeyLen))
	return h, nil
}

// Hash returns the PHC string of password (after NFC) with a new random salt, its Argon2id
// output wrapped for c under the active master key.
func (h *Hasher) Hash(ctx context.Context, c PasswordContext, password string) (string, error) {
	ph := phcHash{params: h.params, salt: randomBytes(int(h.params.SaltLen))}
	key, err := h.derive(ctx, ph, password)
	if err != nil {
		return "", err
	}
	h.keyring.Load().wrapPasswordHash(c, &ph, key)
	return encodePHC(ph), nil
}

// Verify reports whether password matches the PHC string phc stored for c, using the
// parameters and master key named in it. A malformed phc is ErrMalformedHash; one whose master
// key isn't held is ErrKeyNotHeld, and one that doesn't unwrap for c is ErrDecrypt. Those are
// server errors, never a wrong password: unwrapping needs no password.
func (h *Hasher) Verify(ctx context.Context, c PasswordContext, phc, password string) (bool, error) {
	ph, err := decodePHC(phc)
	if err != nil {
		return false, err
	}
	return h.verify(ctx, h.keyring.Load(), c, ph, password)
}

// VerifyDummy does the work of a failed Verify for a user that doesn't exist, so a login for an
// unknown username takes as long as one with a wrong password: an unwrap and Argon2id with the
// same parameters. The dummy is wrapped under the current active key first (microseconds next
// to Argon2id), so it never names a master key that rotation has deleted. It never matches.
func (h *Hasher) VerifyDummy(ctx context.Context, password string) error {
	ring := h.keyring.Load()
	ph := h.dummy
	ring.wrapPasswordHash(dummyPassword, &ph, h.dummyK)
	_, err := h.verify(ctx, ring, dummyPassword, ph, password)
	return err
}

// NeedsRehash reports whether phc was made with Argon2 parameters other than the Hasher's (or
// is malformed), so a successful login should store a new hash. Its master key doesn't count:
// re-wrapping under the active key is rotation's job.
func (h *Hasher) NeedsRehash(phc string) bool {
	ph, err := decodePHC(phc)
	return err != nil || ph.params != h.params
}

// verify unwraps ph's Argon2id output for c (with ring), derives the password's and compares
// them in constant time.
func (h *Hasher) verify(ctx context.Context, ring *Keyring, c PasswordContext, ph phcHash, password string) (bool, error) {
	want, err := ring.unwrapPasswordHash(c, ph)
	if err != nil {
		return false, err
	}
	got, err := h.derive(ctx, ph, password)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// derive runs Argon2id over the NFC password with ph's parameters and salt once a slot is free.
func (h *Hasher) derive(ctx context.Context, ph phcHash, password string) ([]byte, error) {
	t := time.NewTimer(h.maxWait)
	defer t.Stop()
	select {
	case h.sem <- struct{}{}:
	case <-t.C:
		return nil, ErrHasherBusy
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-h.sem }()
	p := ph.params
	return argon2.IDKey([]byte(NormalizePassword(password)), ph.salt, p.Time, p.Memory, p.Threads, p.KeyLen), nil
}

func (p Argon2Params) check() error {
	if p.Memory < 8*uint32(p.Threads) || p.Memory > maxArgon2Memory ||
		p.Time < 1 || p.Time > maxArgon2Time ||
		p.Threads < 1 || p.Threads > maxArgon2Threads ||
		p.SaltLen < minArgon2Bytes || p.SaltLen > maxArgon2Bytes ||
		p.KeyLen < minArgon2Bytes || p.KeyLen > maxArgon2Bytes {
		return fmt.Errorf("auth: argon2 parameters out of range (m=%d t=%d p=%d salt=%d key=%d)",
			p.Memory, p.Time, p.Threads, p.SaltLen, p.KeyLen)
	}
	return nil
}

var phcB64 = base64.RawStdEncoding

// sealedOverhead is what wrapping adds to an Argon2id output in the PHC hash field: the nonce
// and the tag (the envelope's version and key ID are in the PHC string itself).
const sealedOverhead = chacha20poly1305.NonceSizeX + chacha20poly1305.Overhead

// phcHash is a stored password hash: Argon2id's parameters (KeyLen is the unwrapped output's
// length), the ID of the master key it is wrapped under, the salt, and the wrapped output
// (nonce | ciphertext+tag, Keyring.wrapPasswordHash).
type phcHash struct {
	params       Argon2Params
	keyID        uint32
	salt, sealed []byte
}

// encodePHC formats $argon2id$v=19$m=<m>,t=<t>,p=<p>,k=<key id>$<salt>$<wrapped hash>
// (unpadded standard base64). "k" is this server's own parameter: a stock Argon2id verifier
// refuses the string rather than comparing against the wrapped hash.
func encodePHC(h phcHash) string {
	return phcPrefix(h) + "$" + phcB64.EncodeToString(h.sealed)
}

// phcPrefix is the PHC string up to and including the salt: what the wrapped hash is bound to
// besides its row.
func phcPrefix(h phcHash) string {
	p := h.params
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d,k=%d$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, h.keyID, phcB64.EncodeToString(h.salt))
}

// decodePHC parses what encodePHC writes, strictly: the parameters in that order, decimal
// without leading zeros, the bounds of Argon2Params.check (KeyLen being the wrapped hash's
// length less sealedOverhead), and a master key ID from 1 to MaxMasterKeyID.
func decodePHC(s string) (phcHash, error) {
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" ||
		parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return phcHash{}, ErrMalformedHash
	}
	kv := strings.Split(parts[3], ",")
	if len(kv) != 4 {
		return phcHash{}, ErrMalformedHash
	}
	m, okM := phcParam(kv[0], "m=", maxArgon2Memory)
	t, okT := phcParam(kv[1], "t=", maxArgon2Time)
	th, okP := phcParam(kv[2], "p=", maxArgon2Threads)
	k, okK := phcParam(kv[3], "k=", MaxMasterKeyID)
	salt, okSalt := phcBytes(parts[4], maxArgon2Bytes)
	sealed, okSealed := phcBytes(parts[5], maxArgon2Bytes+sealedOverhead)
	if !okM || !okT || !okP || !okK || k == 0 || !okSalt || !okSealed || len(sealed) < sealedOverhead {
		return phcHash{}, ErrMalformedHash
	}
	p := Argon2Params{
		Memory: uint32(m), Time: uint32(t), Threads: uint8(th), //nolint:gosec // G115: bounds-checked above
		SaltLen: uint32(len(salt)), KeyLen: uint32(len(sealed) - sealedOverhead), //nolint:gosec // G115: bounds-checked above
	}
	if p.check() != nil {
		return phcHash{}, ErrMalformedHash
	}
	return phcHash{params: p, keyID: uint32(k), salt: salt, sealed: sealed}, nil //nolint:gosec // G115: phcParam bounds k by MaxMasterKeyID
}

// phcParam parses "<name><decimal>" with no leading zeros and a value at most max.
func phcParam(s, name string, maxVal uint64) (uint64, bool) {
	v, ok := strings.CutPrefix(s, name)
	n, err := strconv.ParseUint(v, 10, 32)
	if !ok || err != nil || strconv.FormatUint(n, 10) != v || n > maxVal {
		return 0, false
	}
	return n, true
}

// phcBytes decodes a salt or hash field of at most maxLen bytes in its one canonical form.
// Strict decoding still skips CR and LF, so the re-encoding must give s back.
func phcBytes(s string, maxLen int) ([]byte, bool) {
	if len(s) > phcB64.EncodedLen(maxLen) {
		return nil, false
	}
	b, err := phcB64.Strict().DecodeString(s)
	if err != nil || phcB64.EncodeToString(b) != s {
		return nil, false
	}
	return b, true
}

// randomBytes returns n bytes from crypto/rand (which never fails; it crashes the program
// instead).
func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}
