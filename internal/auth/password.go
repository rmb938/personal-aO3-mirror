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

// Password length bounds in runes after NFKC. 8 is NIST SP 800-63B-4's minimum when the
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
// control characters, MinPasswordRunes to MaxPasswordRunes runes after NFKC, not the username,
// and not in the common-password list. There are no composition rules. The username and list
// comparisons ignore case. It returns the first rule broken, or nil.
func CheckPassword(password, username string) error {
	if !utf8.ValidString(password) || strings.ContainsFunc(password, unicode.IsControl) {
		return ErrPasswordInvalid
	}
	p := NormalizePassword(password)
	switch n := utf8.RuneCountInString(p); {
	case n < MinPasswordRunes:
		return ErrPasswordTooShort
	case n > MaxPasswordRunes:
		return ErrPasswordTooLong
	}
	folded := strings.ToLower(p)
	if username != "" && folded == strings.ToLower(norm.NFKC.String(username)) {
		return ErrPasswordIsUsername
	}
	if isCommonPassword(folded) {
		return ErrPasswordCommon
	}
	return nil
}

// NormalizePassword is the form a password is hashed and compared in: Unicode NFKC, so the same
// password typed on different keyboards or platforms gives the same bytes (NIST SP 800-63B-4
// §3.1.1.2). Hasher applies it itself; callers need it only for their own comparisons.
func NormalizePassword(password string) string { return norm.NFKC.String(password) }

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

// isCommonPassword reports whether folded (NFKC, lower case) is in the list.
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

// Hasher hashes and verifies passwords with Argon2id in PHC string format. At most Concurrency
// hashes run at once (each holds Params.Memory), so a flood of logins can't exhaust memory; a
// caller that waits longer than MaxWait gets ErrHasherBusy (the API answers 503).
type Hasher struct {
	params  Argon2Params
	sem     chan struct{}
	maxWait time.Duration
	dummy   string // a hash of a random password, verified for unknown users
}

// NewHasher returns a Hasher. It computes the dummy hash, so it costs one hash.
func NewHasher(opts HasherOptions) (*Hasher, error) {
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
	h := &Hasher{params: opts.Params, sem: make(chan struct{}, opts.Concurrency), maxWait: opts.MaxWait}
	h.dummy = encodePHC(h.params, randomBytes(int(h.params.SaltLen)), randomBytes(int(h.params.KeyLen)))
	return h, nil
}

// Hash returns the PHC string of password (after NFKC) with a new random salt.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	salt := randomBytes(int(h.params.SaltLen))
	key, err := h.derive(ctx, h.params, password, salt)
	if err != nil {
		return "", err
	}
	return encodePHC(h.params, salt, key), nil
}

// Verify reports whether password matches the PHC string phc, using the parameters stored in
// it. A malformed phc is ErrMalformedHash.
func (h *Hasher) Verify(ctx context.Context, phc, password string) (bool, error) {
	p, salt, want, err := decodePHC(phc)
	if err != nil {
		return false, err
	}
	got, err := h.derive(ctx, p, password, salt)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// VerifyDummy does the work of a failed Verify for a user that doesn't exist, so a login for an
// unknown username takes as long as one with a wrong password. It never matches.
func (h *Hasher) VerifyDummy(ctx context.Context, password string) error {
	_, err := h.Verify(ctx, h.dummy, password)
	return err
}

// NeedsRehash reports whether phc was made with parameters other than the Hasher's (or is
// malformed), so a successful login should store a new hash.
func (h *Hasher) NeedsRehash(phc string) bool {
	p, _, _, err := decodePHC(phc)
	return err != nil || p != h.params
}

// derive runs Argon2id once a slot is free.
func (h *Hasher) derive(ctx context.Context, p Argon2Params, password string, salt []byte) ([]byte, error) {
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
	return argon2.IDKey([]byte(NormalizePassword(password)), salt, p.Time, p.Memory, p.Threads, p.KeyLen), nil
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

// encodePHC formats $argon2id$v=19$m=<m>,t=<t>,p=<p>$<salt>$<hash> (unpadded standard base64).
func encodePHC(p Argon2Params, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, phcB64.EncodeToString(salt), phcB64.EncodeToString(key))
}

// decodePHC parses what encodePHC writes, strictly: the parameters in that order, decimal
// without leading zeros, and the bounds of Argon2Params.check.
func decodePHC(s string) (Argon2Params, []byte, []byte, error) {
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" ||
		parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return Argon2Params{}, nil, nil, ErrMalformedHash
	}
	kv := strings.Split(parts[3], ",")
	if len(kv) != 3 {
		return Argon2Params{}, nil, nil, ErrMalformedHash
	}
	m, okM := phcParam(kv[0], "m=", maxArgon2Memory)
	t, okT := phcParam(kv[1], "t=", maxArgon2Time)
	th, okP := phcParam(kv[2], "p=", maxArgon2Threads)
	salt, okS := phcBytes(parts[4])
	key, okK := phcBytes(parts[5])
	if !okM || !okT || !okP || !okS || !okK {
		return Argon2Params{}, nil, nil, ErrMalformedHash
	}
	p := Argon2Params{
		Memory: uint32(m), Time: uint32(t), Threads: uint8(th), //nolint:gosec // G115: bounds-checked above
		SaltLen: uint32(len(salt)), KeyLen: uint32(len(key)), //nolint:gosec // G115: bounds-checked above
	}
	if p.check() != nil {
		return Argon2Params{}, nil, nil, ErrMalformedHash
	}
	return p, salt, key, nil
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
