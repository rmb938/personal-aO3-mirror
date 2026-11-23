package auth

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"strings"
)

// TokenKind is an opaque token's prefix, which says what the token is for, so one kind can
// never be presented as another and a leaked token is recognisable.
type TokenKind string

// Token kinds.
const (
	InviteToken     TokenKind = "inv_" // a signup or reset invite
	EnrollmentToken TokenKind = "enr_" // a signup or reset between its two steps
	ChallengeToken  TokenKind = "chl_" // a login between password and TOTP
	RefreshToken    TokenKind = "rt_"  // a session's refresh token
)

// tokenBytes is an opaque token's randomness (256 bits); tokenChars is its base64url length.
const (
	tokenBytes = 32
	tokenChars = 43
)

// NewToken returns a random token of kind k (the prefix, then 32 random bytes in unpadded
// base64url) and the SHA-256 that is stored in place of it. A plain hash is enough: the token
// has 256 bits, so it can't be guessed from the hash.
func NewToken(k TokenKind) (token string, sum [32]byte) {
	token = string(k) + base64.RawURLEncoding.EncodeToString(randomBytes(tokenBytes))
	return token, sha256.Sum256([]byte(token))
}

// TokenSum returns the stored hash of a presented token, or false when the token isn't
// shaped like one of kind k (wrong prefix, length or alphabet), so it can be refused without a
// database read.
func TokenSum(k TokenKind, token string) ([32]byte, bool) {
	body, ok := strings.CutPrefix(token, string(k))
	if !ok || len(body) != tokenChars {
		return [32]byte{}, false
	}
	if b, err := base64.RawURLEncoding.Strict().DecodeString(body); err != nil || len(b) != tokenBytes {
		return [32]byte{}, false
	}
	return sha256.Sum256([]byte(token)), true
}

// Recovery codes: RecoveryCodeCount per user, each 24 base32 characters (120 bits, at least the
// 112 below which NIST SP 800-63B-4 §3.1.2.2 asks for a salted password hashing scheme rather
// than an approved hash) shown as six dash-separated groups of four, lower case.
const (
	RecoveryCodeCount = 10
	recoveryCodeBytes = 15
	recoveryCodeChars = 24
)

var recoveryB32 = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// NewRecoveryCodes returns RecoveryCodeCount new codes in display form (xxxx-xxxx-xxxx-xxxx-xxxx-xxxx).
// Store Keyring.EncryptRecoveryCode of each NormalizeRecoveryCode.
func NewRecoveryCodes() []string {
	codes := make([]string, RecoveryCodeCount)
	for i := range codes {
		s := recoveryB32.EncodeToString(randomBytes(recoveryCodeBytes))
		groups := make([]string, 0, recoveryCodeChars/4)
		for j := 0; j < len(s); j += 4 {
			groups = append(groups, s[j:j+4])
		}
		codes[i] = strings.Join(groups, "-")
	}
	return codes
}

// NormalizeRecoveryCode returns the 24-character form of a typed code: ASCII letters lower-cased,
// dashes and spaces removed. It is false when the result isn't 24 base32 characters. Only ASCII
// is folded (strings.ToLower would take the Kelvin sign U+212A as 'k').
func NormalizeRecoveryCode(code string) (string, bool) {
	if len(code) > 4*recoveryCodeChars {
		return "", false
	}
	s := make([]byte, 0, recoveryCodeChars)
	for i := range len(code) {
		switch c := code[i]; {
		case c == '-' || c == ' ':
		case c >= 'A' && c <= 'Z':
			s = append(s, c-'A'+'a')
		default:
			s = append(s, c)
		}
	}
	if len(s) != recoveryCodeChars {
		return "", false
	}
	// 24 characters are exactly 120 bits, so there are no trailing bits to be non-canonical, and
	// the length check above already counted any CR or LF the decoder would skip.
	if b, err := recoveryB32.DecodeString(string(s)); err != nil || len(b) != recoveryCodeBytes {
		return "", false
	}
	return string(s), true
}
