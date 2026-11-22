package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Access token constants.
const (
	// Audience is every access token's aud.
	Audience = "ao3mirror-api"
	// MaxAccessTokenBytes bounds a presented token before it is parsed.
	MaxAccessTokenBytes = 2048
	// JWTLeeway is the clock skew allowed on exp, nbf and iat. A retired signing key must verify
	// until rotation + access TTL + JWTLeeway.
	JWTLeeway = 30 * time.Second
)

// SigningKey is an Ed25519 key that signs access tokens. ID is the JWT kid. The store keeps the
// seed (Seed); SigningKeyFromSeed rebuilds the key.
type SigningKey struct {
	ID      string
	private ed25519.PrivateKey
}

// NewSigningKey returns a random key with a random kid (16 base64url characters).
func NewSigningKey() SigningKey {
	_, priv, _ := ed25519.GenerateKey(nil) // crypto/rand; never fails
	return SigningKey{ID: base64.RawURLEncoding.EncodeToString(randomBytes(12)), private: priv}
}

// SigningKeyFromSeed rebuilds a stored key.
func SigningKeyFromSeed(id string, seed []byte) (SigningKey, error) {
	if id == "" || len(seed) != ed25519.SeedSize {
		return SigningKey{}, errors.New("auth: malformed signing key")
	}
	return SigningKey{ID: id, private: ed25519.NewKeyFromSeed(seed)}, nil
}

// Seed is the 32-byte private seed to store.
func (k SigningKey) Seed() []byte { return k.private.Seed() }

// VerifyKey is the public half, which the Verifier accepts.
func (k SigningKey) VerifyKey() VerifyKey {
	return VerifyKey{ID: k.ID, Public: k.private.Public().(ed25519.PublicKey)}
}

// VerifyKey is a public key that access tokens may be signed with, by kid. After a rotation the
// caller keeps the old key's VerifyKey until rotated_at + access TTL, then drops it.
type VerifyKey struct {
	ID     string
	Public ed25519.PublicKey
}

// AccessClaims are what an access token says.
type AccessClaims struct {
	UserID    int64
	SessionID int64
	ID        string // jti
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// jwtClaims is the wire form: the registered claims plus sid. sub and sid are decimal IDs.
type jwtClaims struct {
	jwt.RegisteredClaims
	SID string `json:"sid"`
}

// Validate is called by the parser after the registered-claim checks: it requires the claims
// the options can't (iat, sub, sid, jti).
func (c jwtClaims) Validate() error {
	if c.IssuedAt == nil || c.Subject == "" || c.SID == "" || c.ID == "" {
		return jwt.ErrTokenRequiredClaimMissing
	}
	return nil
}

// Issuer signs access tokens.
type Issuer struct {
	issuer string
	key    SigningKey
	ttl    time.Duration
	now    func() time.Time
}

// NewIssuer returns an Issuer. iss is api.public_url; now is the clock (time.Now in production).
func NewIssuer(iss string, key SigningKey, ttl time.Duration, now func() time.Time) *Issuer {
	return &Issuer{issuer: iss, key: key, ttl: ttl, now: now}
}

// Issue signs a token for a user's session, valid from now for the Issuer's TTL.
func (i *Issuer) Issue(userID, sessionID int64) (string, AccessClaims, error) {
	now := i.now().Truncate(time.Second) // NumericDate is whole seconds
	c := AccessClaims{
		UserID: userID, SessionID: sessionID, ID: base64.RawURLEncoding.EncodeToString(randomBytes(16)),
		IssuedAt: now, ExpiresAt: now.Add(i.ttl),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwtClaims{
		Issuer:    i.issuer,
		Subject:   strconv.FormatInt(userID, 10),
		Audience:  jwt.ClaimStrings{Audience},
		ExpiresAt: jwt.NewNumericDate(c.ExpiresAt),
		NotBefore: jwt.NewNumericDate(now),
		IssuedAt:  jwt.NewNumericDate(now),
		ID:        c.ID,
		SID:       strconv.FormatInt(sessionID, 10),
	})
	t.Header["kid"] = i.key.ID
	s, err := t.SignedString(i.key.private)
	if err != nil {
		return "", AccessClaims{}, fmt.Errorf("auth: sign access token: %w", err)
	}
	return s, c, nil
}

// TokenReason says why an access token was refused. It is a closed set, safe to log.
type TokenReason string

// Token refusal reasons.
const (
	TokenMalformed   TokenReason = "malformed"     // not a JWT, too long, or bad claim values
	TokenBadAlg      TokenReason = "bad_alg"       // alg other than EdDSA, or none
	TokenUnknownKey  TokenReason = "unknown_kid"   // no such kid
	TokenBadSig      TokenReason = "bad_signature" // signature doesn't verify
	TokenExpired     TokenReason = "expired"
	TokenNotYetValid TokenReason = "not_yet_valid" // nbf or iat in the future
	TokenBadClaims   TokenReason = "bad_claims"    // wrong iss or aud, or a required claim missing
)

// TokenError is a refused access token.
type TokenError struct {
	Reason TokenReason
	err    error
}

func (e *TokenError) Error() string { return "auth: invalid access token: " + string(e.Reason) }
func (e *TokenError) Unwrap() error { return e.err }

// Verifier checks access tokens. It is immutable: after a key rotation build a new one.
type Verifier struct {
	issuer string
	keys   map[string]ed25519.PublicKey
	parser *jwt.Parser
}

// NewVerifier returns a Verifier that accepts tokens from iss signed by any of keys.
func NewVerifier(iss string, keys []VerifyKey, now func() time.Time) *Verifier {
	m := make(map[string]ed25519.PublicKey, len(keys))
	for _, k := range keys {
		m[k.ID] = k.Public
	}
	return &Verifier{issuer: iss, keys: m, parser: jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(iss),
		jwt.WithAudience(Audience),
		jwt.WithExpirationRequired(),
		jwt.WithNotBeforeRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(JWTLeeway),
		jwt.WithTimeFunc(now),
		jwt.WithStrictDecoding(),
	)}
}

// errUnknownKey is the key function's refusal, mapped to TokenUnknownKey.
var errUnknownKey = errors.New("unknown kid")

// Verify checks a token's signature (EdDSA only, a known kid) and claims (iss, aud, exp, nbf,
// iat, sub, sid, jti), and returns them. Every refusal is a *TokenError.
func (v *Verifier) Verify(token string) (AccessClaims, error) {
	// Only base64url and dots: Go's base64 decoders skip CR and LF, so without this check a
	// signature with a newline in it would verify as the issued one.
	if len(token) > MaxAccessTokenBytes || !compactJWS(token) {
		return AccessClaims{}, &TokenError{Reason: TokenMalformed}
	}
	var c jwtClaims
	_, err := v.parser.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		k, ok := v.keys[kid]
		if !ok {
			return nil, errUnknownKey
		}
		return k, nil
	})
	if err != nil {
		return AccessClaims{}, &TokenError{Reason: tokenReason(err), err: err}
	}
	uid, ok1 := parseID(c.Subject)
	sid, ok2 := parseID(c.SID)
	if !ok1 || !ok2 {
		return AccessClaims{}, &TokenError{Reason: TokenMalformed}
	}
	return AccessClaims{
		UserID: uid, SessionID: sid, ID: c.ID, IssuedAt: c.IssuedAt.Time, ExpiresAt: c.ExpiresAt.Time,
	}, nil
}

// tokenReason maps a parser error to a reason. Signature and key errors come first: claims are
// only meaningful once the signature holds.
func tokenReason(err error) TokenReason {
	switch {
	case errors.Is(err, errUnknownKey):
		return TokenUnknownKey
	case errors.Is(err, jwt.ErrEd25519Verification):
		return TokenBadSig
	case errors.Is(err, jwt.ErrTokenSignatureInvalid), errors.Is(err, jwt.ErrTokenUnverifiable):
		return TokenBadAlg // alg not EdDSA (the parser checks it before the key), or missing
	case errors.Is(err, jwt.ErrTokenMalformed):
		return TokenMalformed
	case errors.Is(err, jwt.ErrTokenExpired):
		return TokenExpired
	case errors.Is(err, jwt.ErrTokenNotValidYet), errors.Is(err, jwt.ErrTokenUsedBeforeIssued):
		return TokenNotYetValid
	default:
		return TokenBadClaims
	}
}

// compactJWS reports whether s holds only the characters of a compact JWS: the base64url
// alphabet and '.'.
func compactJWS(s string) bool {
	for i := range len(s) {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

// parseID parses a positive decimal ID in canonical form (no sign, no leading zeros).
func parseID(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 || strconv.FormatInt(n, 10) != s {
		return 0, false
	}
	return n, true
}
