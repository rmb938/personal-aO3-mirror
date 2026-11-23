package model

import "time"

// The API's accounts (users.db, docs/part2-api.md stage 16). None of this is fanwork. The
// secret fields hold only stored forms (PHC strings, sealed TOTP secrets); zero times mean never.

// User is an account.
type User struct {
	ID                int64
	Username          string // lower case, [a-z0-9_-]{3,32}
	PasswordPHC       string
	TOTPSecretEnc     []byte // sealed for auth.UserTOTP(ID)
	LastTOTPStep      int64  // the last accepted TOTP step; 0 = never
	CreatedAt         time.Time
	LastLoginAt       time.Time
	PasswordChangedAt time.Time
	DisabledAt        time.Time
	FailedAttempts    int       // consecutive failed password or second-factor attempts
	NextAttemptAt     time.Time // backoff: no attempt is checked before this
}

// Disabled reports whether the account is disabled.
func (u User) Disabled() bool { return !u.DisabledAt.IsZero() }

// InviteKind is what an invite is for.
type InviteKind string

const (
	InviteSignup InviteKind = "signup" // a new account
	InviteReset  InviteKind = "reset"  // an admin-issued reset of an existing account
)

// ResetScope is what a reset invite replaces.
type ResetScope string

const (
	ResetPassword ResetScope = "password"
	ResetTOTP     ResetScope = "totp"
	ResetBoth     ResetScope = "both"
)

// ResetScopes lists every ResetScope.
func ResetScopes() []ResetScope { return []ResetScope{ResetPassword, ResetTOTP, ResetBoth} }

// Password reports whether the scope replaces the password.
func (s ResetScope) Password() bool { return s == ResetPassword || s == ResetBoth }

// TOTP reports whether the scope replaces the TOTP secret.
func (s ResetScope) TOTP() bool { return s == ResetTOTP || s == ResetBoth }

// Invite is a signup or reset invite. Its token is stored only as a hash.
type Invite struct {
	ID           int64
	Kind         InviteKind
	TargetUserID int64      // reset: the account; 0 for signup
	Scope        ResetScope // reset only
	CreatedAt    time.Time
	ExpiresAt    time.Time
	UsedAt       time.Time
	UsedBy       int64 // the account it created or reset; 0 when unused or that account is gone
	RevokedAt    time.Time
}

// Enrollment is a signup or reset between its two steps.
type Enrollment struct {
	InviteID      int64
	Username      string // signup only
	PasswordPHC   string // "" = keep the current password (reset)
	TOTPSecretEnc []byte // nil = keep the current TOTP (reset); sealed for auth.EnrollmentTOTP(InviteID)
	ExpiresAt     time.Time
}

// LoginChallenge is a login between its password and second-factor steps.
type LoginChallenge struct {
	UserID    int64
	ExpiresAt time.Time
	Attempts  int // failed second-factor attempts
}

// Session is a logged-in device: one refresh token chain.
type Session struct {
	ID         int64
	UserID     int64
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
	RevokedAt  time.Time
}

// Active reports whether the session can still be used at now.
func (s Session) Active(now time.Time) bool { return s.RevokedAt.IsZero() && now.Before(s.ExpiresAt) }

// SigningKey is a stored access-token signing key.
type SigningKey struct {
	ID        string // the JWT kid
	Seed      []byte // Ed25519 private seed
	CreatedAt time.Time
	RetiredAt time.Time // zero for the active key
}
