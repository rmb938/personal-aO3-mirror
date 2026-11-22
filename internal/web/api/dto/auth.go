package dto

import "time"

// The public auth flows' DTOs (stage 16f). Tokens, secrets and codes are verbatim pattern
// types (verbatim.go); nothing here is fanwork.

// Enrollment is a signup's or reset's first step passed (POST /api/v1/auth/signup,
// /api/v1/auth/reset): confirm EnrollmentToken with a TOTP code before ExpiresAt. TOTPSecret
// and OTPAuthURI are the new TOTP secret to add to an authenticator app; a reset that keeps the
// current secret omits them.
type Enrollment struct {
	EnrollmentToken EnrollmentToken `json:"enrollment_token"`
	TOTPSecret      TOTPSecret      `json:"totp_secret,omitzero"`
	OTPAuthURI      OTPAuthURI      `json:"otpauth_uri,omitzero"`
	ExpiresAt       time.Time       `json:"expires_at"`
}

// RecoveryCodes are single-use recovery codes, shown only in this response (POST
// /api/v1/auth/signup/confirm, /api/v1/auth/reset/confirm). A password-only reset keeps the
// current codes and returns an empty list.
type RecoveryCodes struct {
	RecoveryCodes []RecoveryCode `json:"recovery_codes"`
}

// LoginChallenge is a login's password step passed (POST /api/v1/auth/login): send
// ChallengeToken with a second factor to /api/v1/auth/login/totp before ExpiresAt.
type LoginChallenge struct {
	ChallengeToken ChallengeToken `json:"challenge_token"`
	ExpiresAt      time.Time      `json:"expires_at"`
}

// TokenType is a TokenPair's access token type (RFC 6749 §7.1): always Bearer.
type TokenType string

// TokenTypeBearer is the only TokenType.
const TokenTypeBearer TokenType = "Bearer"

// EnumValues implements the enforcer's enum check.
func (TokenType) EnumValues() []string { return []string{string(TokenTypeBearer)} }

// TokenPair is a session's credentials (POST /api/v1/auth/login/totp, /api/v1/auth/refresh),
// shaped like an RFC 6749 §5.1 token response: send AccessToken as `Authorization: Bearer`
// until it expires (ExpiresIn seconds), then trade RefreshToken for a new pair before
// RefreshExpiresAt. Each refresh token works once.
type TokenPair struct {
	AccessToken      AccessToken  `json:"access_token"`
	TokenType        TokenType    `json:"token_type"`
	ExpiresIn        int64        `json:"expires_in"`
	RefreshToken     RefreshToken `json:"refresh_token"`
	RefreshExpiresAt time.Time    `json:"refresh_expires_at"`
}
