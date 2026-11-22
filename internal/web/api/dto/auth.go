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
