package auth

import (
	"crypto/sha256"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTokens(t *testing.T) {
	for _, k := range []TokenKind{InviteToken, EnrollmentToken, ChallengeToken, RefreshToken} {
		tok, sum := NewToken(k)
		require.Regexp(t, "^"+string(k)+"[A-Za-z0-9_-]{43}$", tok)
		require.Equal(t, sha256.Sum256([]byte(tok)), sum)

		got, ok := TokenSum(k, tok)
		require.True(t, ok)
		require.Equal(t, sum, got)

		other, _ := NewToken(k)
		require.NotEqual(t, tok, other)
	}

	inv, _ := NewToken(InviteToken)
	body := strings.TrimPrefix(inv, string(InviteToken))
	for _, bad := range []struct {
		kind  TokenKind
		token string
	}{
		{RefreshToken, inv},                   // another kind
		{InviteToken, body},                   // no prefix
		{InviteToken, inv[:len(inv)-1]},       // short
		{InviteToken, inv + "A"},              // long
		{InviteToken, inv[:len(inv)-1] + "="}, // not base64url
		{InviteToken, inv[:len(inv)-1] + "+"}, // standard alphabet
		{InviteToken, "INV_" + body},          // prefix case
		{InviteToken, ""},
	} {
		_, ok := TokenSum(bad.kind, bad.token)
		require.False(t, ok, "%s %q", bad.kind, bad.token)
	}
}

// TestTokenSumNonCanonical: 43 base64url characters carry 258 bits, so the last character has
// two unused bits; a token with them set isn't one NewToken made.
func TestTokenSumNonCanonical(t *testing.T) {
	tok, _ := NewToken(RefreshToken)
	last := tok[len(tok)-1]
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	i := strings.IndexByte(alphabet, last)
	alt := tok[:len(tok)-1] + string(alphabet[i^1]) // flips an unused low bit
	_, ok := TokenSum(RefreshToken, alt)
	require.False(t, ok)
}

var recoveryRE = regexp.MustCompile(`^[a-z2-7]{4}-[a-z2-7]{4}-[a-z2-7]{4}-[a-z2-7]{4}$`)

func TestRecoveryCodes(t *testing.T) {
	codes := NewRecoveryCodes()
	require.Len(t, codes, RecoveryCodeCount)
	seen := map[string]bool{}
	for _, c := range codes {
		require.Regexp(t, recoveryRE, c)
		require.False(t, seen[c])
		seen[c] = true

		n, ok := NormalizeRecoveryCode(c)
		require.True(t, ok)
		require.Equal(t, strings.ReplaceAll(c, "-", ""), n)

		typed := strings.ToUpper(strings.ReplaceAll(c, "-", " "))
		n2, ok := NormalizeRecoveryCode(typed)
		require.True(t, ok)
		require.Equal(t, n, n2)
	}

	for _, bad := range []string{
		"", "abcd-efgh-ijkl-mno", "abcd-efgh-ijkl-mnopq", "abcd-efgh-ijkl-mn01", "abcd-efgh-ijkl-mn8p",
		"abcd_efgh_ijkl_mnop", strings.Repeat("-", 100) + "abcdefghijklmnop", "abcd-efgh-ijkl-mnoé",
	} {
		_, ok := NormalizeRecoveryCode(bad)
		require.False(t, ok, "%q", bad)
	}
}
