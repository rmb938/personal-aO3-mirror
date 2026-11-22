package auth

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

const testIss = "https://mirror.example.com"

var jwtNow = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestIssueVerify(t *testing.T) {
	key := NewSigningKey()
	require.Regexp(t, `^[A-Za-z0-9_-]{16}$`, key.ID)
	iss := NewIssuer(testIss, key, 15*time.Minute, fixedClock(jwtNow.Add(400*time.Millisecond)))
	tok, c, err := iss.Issue(42, 7)
	require.NoError(t, err)
	require.Equal(t, jwtNow, c.IssuedAt, "whole seconds")
	require.Equal(t, jwtNow.Add(15*time.Minute), c.ExpiresAt)

	v := NewVerifier(testIss, []VerifyKey{key.VerifyKey()}, fixedClock(jwtNow.Add(time.Minute)))
	got, err := v.Verify(tok)
	require.NoError(t, err)
	require.Equal(t, int64(42), got.UserID)
	require.Equal(t, int64(7), got.SessionID)
	require.Equal(t, c.ID, got.ID)
	require.True(t, c.IssuedAt.Equal(got.IssuedAt))
	require.True(t, c.ExpiresAt.Equal(got.ExpiresAt))

	tok2, c2, err := iss.Issue(42, 7)
	require.NoError(t, err)
	require.NotEqual(t, tok, tok2)
	require.NotEqual(t, c.ID, c2.ID, "jti is unique")

	// The header names the key and the algorithm.
	p, _, err := jwt.NewParser().ParseUnverified(tok, jwt.MapClaims{})
	require.NoError(t, err)
	require.Equal(t, "EdDSA", p.Header["alg"])
	require.Equal(t, key.ID, p.Header["kid"])
}

func TestVerifyTimes(t *testing.T) {
	key := NewSigningKey()
	tok, _, err := NewIssuer(testIss, key, 15*time.Minute, fixedClock(jwtNow)).Issue(1, 1)
	require.NoError(t, err)
	for _, tc := range []struct {
		at   time.Duration
		want TokenReason
	}{
		{-30 * time.Second, ""}, // within the leeway
		{-31 * time.Second, TokenNotYetValid},
		{15*time.Minute + 29*time.Second, ""}, // exp is exclusive: exp+leeway is expired
		{15*time.Minute + 30*time.Second, TokenExpired},
	} {
		v := NewVerifier(testIss, []VerifyKey{key.VerifyKey()}, fixedClock(jwtNow.Add(tc.at)))
		_, err := v.Verify(tok)
		requireReason(t, err, tc.want)
	}
}

func TestVerifyKeyRotation(t *testing.T) {
	oldKey, newKey := NewSigningKey(), NewSigningKey()
	clock := fixedClock(jwtNow)
	oldTok, _, err := NewIssuer(testIss, oldKey, time.Minute, clock).Issue(1, 1)
	require.NoError(t, err)
	newTok, _, err := NewIssuer(testIss, newKey, time.Minute, clock).Issue(1, 2)
	require.NoError(t, err)

	both := NewVerifier(testIss, []VerifyKey{newKey.VerifyKey(), oldKey.VerifyKey()}, clock)
	_, err = both.Verify(oldTok)
	require.NoError(t, err)
	_, err = both.Verify(newTok)
	require.NoError(t, err)

	newOnly := NewVerifier(testIss, []VerifyKey{newKey.VerifyKey()}, clock)
	_, err = newOnly.Verify(oldTok)
	requireReason(t, err, TokenUnknownKey)

	// A key rebuilt from its stored seed signs the same way.
	again, err := SigningKeyFromSeed(oldKey.ID, oldKey.Seed())
	require.NoError(t, err)
	require.Equal(t, oldKey.VerifyKey(), again.VerifyKey())
	_, err = SigningKeyFromSeed("", oldKey.Seed())
	require.Error(t, err)
	_, err = SigningKeyFromSeed("x", oldKey.Seed()[:31])
	require.Error(t, err)
}

// TestVerifyRejects covers every refusal named in the stage 16 plan.
func TestVerifyRejects(t *testing.T) {
	key := NewSigningKey()
	v := NewVerifier(testIss, []VerifyKey{key.VerifyKey()}, fixedClock(jwtNow))
	good := func() jwt.MapClaims {
		return jwt.MapClaims{
			"iss": testIss, "aud": Audience, "sub": "1", "sid": "2", "jti": "fixture-jti",
			"iat": jwtNow.Unix(), "nbf": jwtNow.Unix(), "exp": jwtNow.Add(time.Minute).Unix(),
		}
	}
	with := func(k string, val any) jwt.MapClaims {
		c := good()
		if val == nil {
			delete(c, k)
		} else {
			c[k] = val
		}
		return c
	}
	// The control: a hand-built token with good claims verifies.
	_, err := v.Verify(signEdDSA(t, key.private, key.ID, good()))
	require.NoError(t, err)

	other := NewSigningKey()
	pub := key.VerifyKey().Public
	for name, tc := range map[string]struct {
		token string
		want  TokenReason
	}{
		"alg none":            {signNone(t, key.ID, good()), TokenBadAlg},
		"HS256 keyed with pk": {signHS256(t, pub, key.ID, good()), TokenBadAlg},
		"unknown kid":         {signEdDSA(t, other.private, other.ID, good()), TokenUnknownKey},
		"no kid":              {signEdDSA(t, key.private, "", good()), TokenUnknownKey},
		"other key, same kid": {signEdDSA(t, other.private, key.ID, good()), TokenBadSig},
		"wrong iss":           {signEdDSA(t, key.private, key.ID, with("iss", "https://evil.example")), TokenBadClaims},
		"no iss":              {signEdDSA(t, key.private, key.ID, with("iss", nil)), TokenBadClaims},
		"wrong aud":           {signEdDSA(t, key.private, key.ID, with("aud", "other-api")), TokenBadClaims},
		"no aud":              {signEdDSA(t, key.private, key.ID, with("aud", nil)), TokenBadClaims},
		"expired":             {signEdDSA(t, key.private, key.ID, with("exp", jwtNow.Add(-time.Minute).Unix())), TokenExpired},
		"no exp":              {signEdDSA(t, key.private, key.ID, with("exp", nil)), TokenBadClaims},
		"future nbf":          {signEdDSA(t, key.private, key.ID, with("nbf", jwtNow.Add(time.Minute).Unix())), TokenNotYetValid},
		"no nbf":              {signEdDSA(t, key.private, key.ID, with("nbf", nil)), TokenBadClaims},
		"future iat":          {signEdDSA(t, key.private, key.ID, with("iat", jwtNow.Add(time.Minute).Unix())), TokenNotYetValid},
		"no iat":              {signEdDSA(t, key.private, key.ID, with("iat", nil)), TokenBadClaims},
		"no sub":              {signEdDSA(t, key.private, key.ID, with("sub", nil)), TokenBadClaims},
		"no sid":              {signEdDSA(t, key.private, key.ID, with("sid", nil)), TokenBadClaims},
		"no jti":              {signEdDSA(t, key.private, key.ID, with("jti", nil)), TokenBadClaims},
		"sub not canonical":   {signEdDSA(t, key.private, key.ID, with("sub", "01")), TokenMalformed},
		"sub zero":            {signEdDSA(t, key.private, key.ID, with("sub", "0")), TokenMalformed},
		"sid negative":        {signEdDSA(t, key.private, key.ID, with("sid", "-2")), TokenMalformed},
		"sid number":          {signEdDSA(t, key.private, key.ID, with("sid", 2)), TokenMalformed},
		"garbage":             {"not.a.jwt", TokenMalformed},
		"empty":               {"", TokenMalformed},
		"too long":            {strings.Repeat("a", MaxAccessTokenBytes+1), TokenMalformed},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := v.Verify(tc.token)
			requireReason(t, err, tc.want)
		})
	}

	// A tampered signature or payload.
	tok := signEdDSA(t, key.private, key.ID, good())
	parts := strings.Split(tok, ".")
	sig := []byte(parts[2])
	sig[0] ^= 'A' ^ 'B'
	_, err = v.Verify(parts[0] + "." + parts[1] + "." + string(sig))
	requireReason(t, err, TokenBadSig)
	forged := signEdDSA(t, other.private, key.ID, with("sub", "99"))
	_, err = v.Verify(parts[0] + "." + strings.Split(forged, ".")[1] + "." + parts[2])
	requireReason(t, err, TokenBadSig)
}

func requireReason(t *testing.T, err error, want TokenReason) {
	t.Helper()
	if want == "" {
		require.NoError(t, err)
		return
	}
	var te *TokenError
	require.ErrorAs(t, err, &te)
	require.Equal(t, want, te.Reason, "%v", err)
}

func signEdDSA(t *testing.T, k ed25519.PrivateKey, kid string, c jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c)
	if kid != "" {
		tok.Header["kid"] = kid
	}
	s, err := tok.SignedString(k)
	require.NoError(t, err)
	return s
}

func signNone(t *testing.T, kid string, c jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, c)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)
	return s
}

// signHS256 is the classic key-confusion attack: HMAC keyed with the server's public key.
func signHS256(t *testing.T, pub ed25519.PublicKey, kid string, c jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	tok.Header["kid"] = kid
	s, err := tok.SignedString([]byte(pub))
	require.NoError(t, err)
	return s
}
