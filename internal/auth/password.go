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

// Hasher hashes and verifies passwords with Argon2id in PHC string format. Before Argon2id the
// NFC password is peppered: HMAC-SHA256 under a key from the Keyring's active master key (NIST
// SP 800-63B-4 §3.1.1.2's keyed hashing step, with the key kept apart from the hashes), so a
// copy of users.db alone allows no offline guessing. The PHC string names that master key
// ("k=<id>"), so a hash under another key can be told apart and rehashed on login.
//
// At most Concurrency hashes run at once (each holds Params.Memory), so a flood of logins can't
// exhaust memory; a caller that waits longer than MaxWait gets ErrHasherBusy (the API answers
// 503).
type Hasher struct {
	keyring *Keyring
	params  Argon2Params
	sem     chan struct{}
	maxWait time.Duration
	dummy   string // a hash of a random password, verified for unknown users
}

// NewHasher returns a Hasher peppering with keyring's active master key. It builds the dummy
// hash without running Argon2id.
func NewHasher(keyring *Keyring, opts HasherOptions) (*Hasher, error) {
	if keyring == nil {
		return nil, errors.New("auth: NewHasher needs the Keyring (for the password pepper)")
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
	h.dummy = encodePHC(phcHash{
		params: h.params, pepper: keyring.active,
		salt: randomBytes(int(h.params.SaltLen)), key: randomBytes(int(h.params.KeyLen)),
	})
	return h, nil
}

// Hash returns the PHC string of password (after NFC, peppered) with a new random salt.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	ph := phcHash{params: h.params, pepper: h.keyring.active, salt: randomBytes(int(h.params.SaltLen))}
	key, err := h.derive(ctx, ph, password)
	if err != nil {
		return "", err
	}
	ph.key = key
	return encodePHC(ph), nil
}

// Verify reports whether password matches the PHC string phc, using the parameters and pepper
// key stored in it. A malformed phc, or one naming a master key the Keyring doesn't hold, is
// ErrMalformedHash.
func (h *Hasher) Verify(ctx context.Context, phc, password string) (bool, error) {
	ph, err := decodePHC(phc)
	if err != nil {
		return false, err
	}
	got, err := h.derive(ctx, ph, password)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, ph.key) == 1, nil
}

// VerifyDummy does the work of a failed Verify for a user that doesn't exist, so a login for an
// unknown username takes as long as one with a wrong password. It never matches.
func (h *Hasher) VerifyDummy(ctx context.Context, password string) error {
	_, err := h.Verify(ctx, h.dummy, password)
	return err
}

// NeedsRehash reports whether phc was made with parameters or a master key other than the
// Hasher's (or is malformed), so a successful login should store a new hash.
func (h *Hasher) NeedsRehash(phc string) bool {
	ph, err := decodePHC(phc)
	return err != nil || ph.params != h.params || ph.pepper != h.keyring.active
}

// derive peppers the NFC password with ph's master key's pepper, then runs Argon2id with ph's
// parameters and salt once a slot is free.
func (h *Hasher) derive(ctx context.Context, ph phcHash, password string) ([]byte, error) {
	pepper, ok := h.keyring.pepperKey(ph.pepper)
	if !ok {
		return nil, ErrMalformedHash
	}
	peppered := mac32(pepper, NormalizePassword(password))
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
	return argon2.IDKey(peppered[:], ph.salt, p.Time, p.Memory, p.Threads, p.KeyLen), nil
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

// phcHash is a stored password hash: Argon2id's parameters, the ID of the master key whose
// pepper it used (Keyring.pepperKey), the salt and the derived key.
type phcHash struct {
	params    Argon2Params
	pepper    uint32 // a master key ID
	salt, key []byte
}

// encodePHC formats $argon2id$v=19$m=<m>,t=<t>,p=<p>,k=<key id>$<salt>$<hash> (unpadded
// standard base64). "k" is this server's own parameter: a stock Argon2id verifier refuses the
// string rather than checking it without the pepper.
func encodePHC(h phcHash) string {
	p := h.params
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d,k=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, h.pepper,
		phcB64.EncodeToString(h.salt), phcB64.EncodeToString(h.key))
}

// decodePHC parses what encodePHC writes, strictly: the parameters in that order, decimal
// without leading zeros, the bounds of Argon2Params.check, and a master key ID from 1 to
// MaxMasterKeyID.
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
	salt, okSalt := phcBytes(parts[4])
	key, okKey := phcBytes(parts[5])
	if !okM || !okT || !okP || !okK || k == 0 || !okSalt || !okKey {
		return phcHash{}, ErrMalformedHash
	}
	p := Argon2Params{
		Memory: uint32(m), Time: uint32(t), Threads: uint8(th), //nolint:gosec // G115: bounds-checked above
		SaltLen: uint32(len(salt)), KeyLen: uint32(len(key)), //nolint:gosec // G115: bounds-checked above
	}
	if p.check() != nil {
		return phcHash{}, ErrMalformedHash
	}
	return phcHash{params: p, pepper: uint32(k), salt: salt, key: key}, nil //nolint:gosec // G115: phcParam bounds k by MaxMasterKeyID
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

// phcBytes decodes a salt or hash field of at most maxArgon2Bytes in its one canonical form.
// Strict decoding still skips CR and LF, so the re-encoding must give s back.
func phcBytes(s string) ([]byte, bool) {
	if len(s) > phcB64.EncodedLen(maxArgon2Bytes) {
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
