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

// Recovery codes: RecoveryCodeCount per user, each 16 base32 characters (80 bits) shown as
// four dash-separated groups of four, lower case.
const (
	RecoveryCodeCount = 10
	recoveryCodeBytes = 10
	recoveryCodeChars = 16
)

var recoveryB32 = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// NewRecoveryCodes returns RecoveryCodeCount new codes in display form (xxxx-xxxx-xxxx-xxxx).
// Store RecoveryCodeMAC of each NormalizeRecoveryCode.
func NewRecoveryCodes() []string {
	codes := make([]string, RecoveryCodeCount)
	for i := range codes {
		s := recoveryB32.EncodeToString(randomBytes(recoveryCodeBytes))
		codes[i] = s[0:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16]
	}
	return codes
}

// NormalizeRecoveryCode returns the 16-character form of a typed code: case folded, dashes and
// spaces removed. It is false when the result isn't 16 base32 characters.
func NormalizeRecoveryCode(code string) (string, bool) {
	if len(code) > 4*recoveryCodeChars {
		return "", false
	}
	s := strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(code))
	if len(s) != recoveryCodeChars {
		return "", false
	}
	// 16 characters are exactly 80 bits, so there are no trailing bits to be non-canonical.
	if b, err := recoveryB32.DecodeString(s); err != nil || len(b) != recoveryCodeBytes {
		return "", false
	}
	return s, true
}
