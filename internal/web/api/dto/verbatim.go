package dto

// Verbatim pattern types (stage 16): server-generated or strictly shaped values that are never
// fanwork, so the enforcer passes them unhashed, but only on a full match of VerbatimPattern
// (else the response is a 500). Each satisfies redact.Verbatim structurally. Keep every pattern
// as tight as its value allows, and never give one to a field that could hold fanwork or free
// text. The formats come from internal/auth; runtime's tests check real values against them.

// Username is an account name: 3..32 of [a-z0-9_-], case-folded at signup.
type Username string

// VerbatimPattern implements redact.Verbatim.
func (Username) VerbatimPattern() string { return `[a-z0-9_-]{3,32}` }

// AccessToken is an EdDSA JWT in compact form. The header is always
// {"alg":"EdDSA","kid":"<16 base64url>","typ":"JWT"} (sorted keys), so its base64url is fixed
// but for the kid; the claims are a JSON object (base64url "eyJ" is `{"`); the signature is 64
// bytes, 86 characters whose last holds 2 bits.
type AccessToken string

// VerbatimPattern implements redact.Verbatim.
func (AccessToken) VerbatimPattern() string {
	return `eyJhbGciOiJFZERTQSIsImtpZCI6I[A-Za-z0-9_-]{22}iLCJ0eXAiOiJKV1QifQ` +
		`\.eyJ[A-Za-z0-9_-]{16,700}` +
		`\.[A-Za-z0-9_-]{85}[AQgw]`
}

// EnrollmentToken is an opaque token for a signup or reset between its two steps.
type EnrollmentToken string

// VerbatimPattern implements redact.Verbatim.
func (EnrollmentToken) VerbatimPattern() string { return `enr_` + opaqueBody }

// ChallengeToken is an opaque token for a login between its password and TOTP steps.
type ChallengeToken string

// VerbatimPattern implements redact.Verbatim.
func (ChallengeToken) VerbatimPattern() string { return `chl_` + opaqueBody }

// RefreshToken is a session's opaque refresh token.
type RefreshToken string

// VerbatimPattern implements redact.Verbatim.
func (RefreshToken) VerbatimPattern() string { return `rt_` + opaqueBody }

// opaqueBody is an opaque token after its prefix: 32 bytes in unpadded base64url (43
// characters; the last holds 4 bits).
const opaqueBody = `[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]`

// TOTPSecret is a 20-byte TOTP secret in unpadded RFC 4648 base32, as typed into an
// authenticator app.
type TOTPSecret string

// VerbatimPattern implements redact.Verbatim.
func (TOTPSecret) VerbatimPattern() string { return totpBase32 }

// totpBase32 is 20 bytes in unpadded base32: exactly 32 characters, no spare bits.
const totpBase32 = `[A-Z2-7]{32}`

// OTPAuthURI is the otpauth:// URI an authenticator app scans: label "issuer:username", then
// the secret, issuer and fixed parameters. The issuer is config (totp_issuer,
// [A-Za-z0-9 ._-]{1,32}, spaces as %20).
type OTPAuthURI string

// VerbatimPattern implements redact.Verbatim.
func (OTPAuthURI) VerbatimPattern() string {
	const issuer = `(?:[A-Za-z0-9._-]|%20){1,32}`
	return `otpauth://totp/` + issuer + `:[a-z0-9_-]{3,32}\?secret=` + totpBase32 + `&issuer=` + issuer +
		`&algorithm=SHA1&digits=6&period=30`
}

// RecoveryCode is a single-use recovery code in display form: six dash-separated groups of
// four lower-case base32 characters.
type RecoveryCode string

// VerbatimPattern implements redact.Verbatim.
func (RecoveryCode) VerbatimPattern() string { return `[a-z2-7]{4}(?:-[a-z2-7]{4}){5}` }

// InviteURL is a signup or reset link: api.public_url (scheme, lower-case host or bracketed
// IPv6, optional port; no path), the page, and the invite token in the fragment.
type InviteURL string

// VerbatimPattern implements redact.Verbatim.
func (InviteURL) VerbatimPattern() string {
	const host = `(?:(?:[a-z0-9-]{1,63}\.){0,7}[a-z0-9-]{1,63}|\[[0-9a-f:.]{2,45}\])`
	return `https?://` + host + `(?::[0-9]{1,5})?/(?:signup|reset)#token=inv_` + opaqueBody
}
