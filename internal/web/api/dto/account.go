package dto

import "time"

// The admin socket's account DTOs (stage 16). None of this is fanwork: usernames and invite
// links are verbatim pattern types (verbatim.go), everything else is an ID, enum or time.

// Items is a whole, unpaged list: {items}. The account lists are small (one owner's invites,
// users and sessions), so they are not paged.
type Items[T any] struct {
	Items []T `json:"items"`
}

// InviteKind is what an invite is for (an enum).
type InviteKind string

// EnumValues lists every InviteKind.
func (InviteKind) EnumValues() []string { return []string{"signup", "reset"} }

// ResetScope is what a reset invite replaces (an enum).
type ResetScope string

// EnumValues lists every ResetScope.
func (ResetScope) EnumValues() []string { return []string{"password", "totp", "both"} }

// Invite is a signup or reset invite (GET /api/v1/admin/invites). Its token is never shown
// again after it is created.
type Invite struct {
	ID   int64      `json:"id"`
	Kind InviteKind `json:"kind"`
	// UserID is a reset's account (omitted for a signup).
	UserID int64 `json:"user_id,omitzero"`
	// Scope is what a reset replaces (omitted for a signup).
	Scope     ResetScope `json:"scope,omitzero"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    time.Time  `json:"used_at,omitzero"`
	// UsedBy is the account the invite created or reset (omitted when unused, or when that
	// account was deleted).
	UsedBy    int64     `json:"used_by,omitzero"`
	RevokedAt time.Time `json:"revoked_at,omitzero"`
}

// InviteLink is a new signup or reset invite (POST /api/v1/admin/invites,
// /api/v1/admin/users/{id}/reset): the link to hand to the person, shown only here.
type InviteLink struct {
	ID        int64     `json:"id"`
	URL       InviteURL `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// User is an account without its secrets (GET /api/v1/admin/users).
type User struct {
	ID                int64     `json:"id"`
	Username          Username  `json:"username"`
	CreatedAt         time.Time `json:"created_at"`
	LastLoginAt       time.Time `json:"last_login_at,omitzero"`
	PasswordChangedAt time.Time `json:"password_changed_at,omitzero"`
	// DisabledAt is when the account was disabled (omitted when it is enabled).
	DisabledAt time.Time `json:"disabled_at,omitzero"`
	// FailedAttempts counts consecutive failed password or second-factor attempts.
	FailedAttempts    int `json:"failed_attempts"`
	RecoveryCodesLeft int `json:"recovery_codes_left"`
}

// Session is an active logged-in device (GET /api/v1/admin/users/{id}/sessions).
type Session struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	// LastUsedAt is the last login or refresh.
	LastUsedAt time.Time `json:"last_used_at"`
	// ExpiresAt is when the session ends unless it is refreshed (never past the login plus
	// auth.max_session_age).
	ExpiresAt time.Time `json:"expires_at"`
}
