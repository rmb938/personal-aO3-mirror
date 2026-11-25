package request

import (
	"slices"
	"strconv"
	"strings"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
)

// The public auth flows' bodies (stage 16f). These checks only bound the input and refuse
// shapes that can never be valid; the Auth controller checks the tokens, the username and the
// password policy (whose errors the api package maps onto the same members).

// Bounds of the auth bodies.
const (
	// MinUsernameLen and MaxUsernameLen bound a username: [A-Za-z0-9_-], lower-cased at signup.
	MinUsernameLen = 3
	MaxUsernameLen = 32
	// MaxPasswordBytes bounds a password before the controller's policy check (8..128 runes
	// after NFC, which can merge several input runes into one).
	MaxPasswordBytes = 2048
	// TOTPDigits is a TOTP code's length.
	TOTPDigits = 6
	// opaqueTokenLen is an opaque token after its prefix: 32 bytes of unpadded base64url.
	opaqueTokenLen = 43
)

// The opaque token prefixes (internal/auth's TokenKind values).
const (
	prefixInvite     = "inv_"
	prefixEnrollment = "enr_"
	prefixChallenge  = "chl_"
	prefixRefresh    = "rt_"
)

// Recovery code shape (internal/auth's NormalizeRecoveryCode): 24 base32 characters in either
// case, with any dashes and spaces, at most maxRecoveryCodeBytes as typed.
const (
	recoveryCodeChars    = 24
	maxRecoveryCodeBytes = 4 * recoveryCodeChars
)

var (
	detailInviteToken     = tokenDetail(prefixInvite, "the invite link's token, after #token=")
	detailEnrollmentToken = tokenDetail(prefixEnrollment, "the enrollment_token the first step returned")
	// DetailUsername is also the api package's detail for the controller's ErrInvalidUsername.
	DetailUsername = problem.NewDetail("must be " + strconv.Itoa(MinUsernameLen) + " to " + strconv.Itoa(MaxUsernameLen) +
		" characters of a-z, 0-9, _ and - (letters in either case; stored lower-case)")
	// The password policy itself is checked by the controller (runtime's DetailPassword*).
	detailPassword      = problem.NewDetail("is required, at most " + strconv.Itoa(MaxPasswordBytes) + " bytes")
	detailResetPassword = problem.NewDetail("must be at most " + strconv.Itoa(MaxPasswordBytes) + " bytes")
	detailTOTPCode      = problem.NewDetail("must be the " + strconv.Itoa(TOTPDigits) + "-digit code from the authenticator app")
	detailChallenge     = tokenDetail(prefixChallenge, "the challenge_token the login returned")
	detailRefreshToken  = tokenDetail(prefixRefresh, "the refresh_token the login or the last refresh returned")
	detailRecoveryCode  = problem.NewDetail("must be a recovery code: " + strconv.Itoa(recoveryCodeChars) +
		" characters of a-z and 2-7 (either case; dashes and spaces are ignored)")
	detailSecondFactor = problem.NewDetail("exactly one of code and recovery_code is required")
	detailTransport    = problem.OneOf(dto.Transport(""))
	// detailCookieRefresh: with the cookie transport the refresh token is the refresh cookie.
	detailCookieRefresh = problem.NewDetail("must be omitted when transport is cookie: the refresh cookie is used")
)

// validTransport reports whether t is omitted (bearer) or one of the dto.Transport values.
func validTransport(t dto.Transport) bool {
	return t == "" || slices.Contains(t.EnumValues(), string(t))
}

func tokenDetail(prefix, what string) problem.Detail {
	return problem.NewDetail("must be " + what + ": " + prefix + " then " + strconv.Itoa(opaqueTokenLen) +
		" base64url characters")
}

// validToken reports whether s has the shape of an opaque token with prefix.
func validToken(s, prefix string) bool {
	body, ok := strings.CutPrefix(s, prefix)
	if !ok || len(body) != opaqueTokenLen {
		return false
	}
	for i := 0; i < len(body); i++ {
		c := body[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// validUsername reports whether s is MinUsernameLen..MaxUsernameLen of [A-Za-z0-9_-].
func validUsername(s string) bool {
	if len(s) < MinUsernameLen || len(s) > MaxUsernameLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// validPassword reports whether s is set and within MaxPasswordBytes.
func validPassword(s string) bool { return s != "" && len(s) <= MaxPasswordBytes }

// validTOTPCode reports whether s is TOTPDigits ASCII digits.
func validTOTPCode(s string) bool {
	if len(s) != TOTPDigits {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Signup is the body of POST /api/v1/auth/signup: an invite, a username and a password.
type Signup struct {
	InviteToken string `json:"invite_token"`
	Username    string `json:"username"`
	Password    string `json:"password"`
}

// Validate records a malformed invite token, username or password.
func (s *Signup) Validate(v *problem.Validation) {
	if !validToken(s.InviteToken, prefixInvite) {
		v.Member(detailInviteToken, "invite_token")
	}
	if !validUsername(s.Username) {
		v.Member(DetailUsername, "username")
	}
	if !validPassword(s.Password) {
		v.Member(detailPassword, "password")
	}
}

// Reset is the body of POST /api/v1/auth/reset: a reset link's token and, when its scope
// replaces the password, the new password.
type Reset struct {
	InviteToken string `json:"invite_token"`
	Password    string `json:"password,omitzero"`
}

// Validate records a malformed invite token or an over-long password.
func (s *Reset) Validate(v *problem.Validation) {
	if !validToken(s.InviteToken, prefixInvite) {
		v.Member(detailInviteToken, "invite_token")
	}
	if len(s.Password) > MaxPasswordBytes {
		v.Member(detailResetPassword, "password")
	}
}

// ConfirmEnrollment is the body of POST /api/v1/auth/signup/confirm and
// /api/v1/auth/reset/confirm: the first step's enrollment token and a TOTP code (from the new
// secret, or from the current one when a reset keeps it).
type ConfirmEnrollment struct {
	EnrollmentToken string `json:"enrollment_token"`
	Code            string `json:"code"`
}

// Validate records a malformed enrollment token or code.
func (s *ConfirmEnrollment) Validate(v *problem.Validation) {
	if !validToken(s.EnrollmentToken, prefixEnrollment) {
		v.Member(detailEnrollmentToken, "enrollment_token")
	}
	if !validTOTPCode(s.Code) {
		v.Member(detailTOTPCode, "code")
	}
}

// validRecoveryCode reports whether s, without dashes and spaces, is recoveryCodeChars of
// base32 in either case.
func validRecoveryCode(s string) bool {
	if len(s) > maxRecoveryCodeBytes {
		return false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '-' || c == ' ':
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '2' && c <= '7':
			n++
		default:
			return false
		}
	}
	return n == recoveryCodeChars
}

// Login is the body of POST /api/v1/auth/login: a username and password. A malformed username
// is refused here (no account can have it); any other unknown one is the controller's 401.
type Login struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Validate records a malformed username or password.
func (s *Login) Validate(v *problem.Validation) {
	if !validUsername(s.Username) {
		v.Member(DetailUsername, "username")
	}
	if !validPassword(s.Password) {
		v.Member(detailPassword, "password")
	}
}

// LoginTOTP is the body of POST /api/v1/auth/login/totp: the login's challenge token, exactly
// one of a TOTP code and a recovery code, and how to hand over the tokens (omitted: bearer).
type LoginTOTP struct {
	ChallengeToken string        `json:"challenge_token"`
	Code           string        `json:"code,omitzero"`
	RecoveryCode   string        `json:"recovery_code,omitzero"`
	Transport      dto.Transport `json:"transport,omitzero"`
}

// Validate records a malformed challenge token, no or both second factors, a malformed one, or
// an unknown transport.
func (s *LoginTOTP) Validate(v *problem.Validation) {
	if !validToken(s.ChallengeToken, prefixChallenge) {
		v.Member(detailChallenge, "challenge_token")
	}
	if !validTransport(s.Transport) {
		v.Member(detailTransport, "transport")
	}
	switch {
	case s.Code == "" && s.RecoveryCode == "":
		v.Member(detailSecondFactor, "code")
	case s.Code != "" && s.RecoveryCode != "":
		v.Member(detailSecondFactor, "recovery_code")
	case s.Code != "" && !validTOTPCode(s.Code):
		v.Member(detailTOTPCode, "code")
	case s.RecoveryCode != "" && !validRecoveryCode(s.RecoveryCode):
		v.Member(detailRecoveryCode, "recovery_code")
	}
}

// Refresh is the body of POST /api/v1/auth/refresh: the session's current refresh token, or
// with the cookie transport no token (the refresh cookie holds it).
type Refresh struct {
	RefreshToken string        `json:"refresh_token,omitzero"`
	Transport    dto.Transport `json:"transport,omitzero"`
}

// Validate records an unknown transport, a missing or malformed refresh token (bearer), or one
// sent with the cookie transport.
func (s *Refresh) Validate(v *problem.Validation) {
	if !validTransport(s.Transport) {
		v.Member(detailTransport, "transport")
	}
	switch {
	case s.Transport == dto.TransportCookie:
		if s.RefreshToken != "" {
			v.Member(detailCookieRefresh, "refresh_token")
		}
	case !validToken(s.RefreshToken, prefixRefresh):
		v.Member(detailRefreshToken, "refresh_token")
	}
}

// ChangePassword is the body of PUT /api/v1/account/password: the current password, the new
// one and a TOTP code.
type ChangePassword struct {
	CurrentPassword string `json:"current_password"`
	Password        string `json:"password"`
	Code            string `json:"code"`
}

// Validate records a missing or over-long password or a malformed code.
func (s *ChangePassword) Validate(v *problem.Validation) {
	if !validPassword(s.CurrentPassword) {
		v.Member(detailPassword, "current_password")
	}
	if !validPassword(s.Password) {
		v.Member(detailPassword, "password")
	}
	if !validTOTPCode(s.Code) {
		v.Member(detailTOTPCode, "code")
	}
}

// RegenerateRecoveryCodes is the body of POST /api/v1/account/recovery-codes: a TOTP code.
type RegenerateRecoveryCodes struct {
	Code string `json:"code"`
}

// Validate records a malformed code.
func (s *RegenerateRecoveryCodes) Validate(v *problem.Validation) {
	if !validTOTPCode(s.Code) {
		v.Member(detailTOTPCode, "code")
	}
}
