package request

import (
	"slices"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
)

// MinInviteTTL and MaxInviteTTL bound an invite's ttl. They equal controllers.MinInviteTTL
// and MaxInviteTTL (this package imports only the standard library; a convert test checks).
const (
	MinInviteTTL = time.Minute
	MaxInviteTTL = 7 * 24 * time.Hour
)

var (
	detailInviteTTL = problem.NewDetail("must be omitted or a duration from " + MinInviteTTL.String() +
		" to " + MaxInviteTTL.String())
	detailResetScope = problem.OneOf(dto.ResetScope(""))
)

// validateInviteTTL records d at key when it is set (non-zero) and out of range.
func validateInviteTTL(v *problem.Validation, d dto.Duration, key string) {
	if t := d.Duration(); t != 0 && (t < MinInviteTTL || t > MaxInviteTTL) {
		v.Member(detailInviteTTL, key)
	}
}

// CreateInvite is the body of POST /api/v1/admin/invites (admin socket). `{}` uses the
// configured auth.invite_ttl.
type CreateInvite struct {
	// TTL is how long the invite stays usable; absent (or zero) is auth.invite_ttl.
	TTL dto.Duration `json:"ttl,omitzero"`
}

// Validate records an out-of-range ttl.
func (c *CreateInvite) Validate(v *problem.Validation) { validateInviteTTL(v, c.TTL, "ttl") }

// CreateReset is the body of POST /api/v1/admin/users/{id}/reset (admin socket).
type CreateReset struct {
	// Scope is what the reset replaces: the password, the TOTP secret or both (required).
	Scope dto.ResetScope `json:"scope"`
	// TTL is how long the reset link stays usable; absent (or zero) is auth.invite_ttl.
	TTL dto.Duration `json:"ttl,omitzero"`
}

// Validate records a missing or unknown scope and an out-of-range ttl.
func (c *CreateReset) Validate(v *problem.Validation) {
	if !slices.Contains(c.Scope.EnumValues(), string(c.Scope)) {
		v.Member(detailResetScope, "scope")
	}
	validateInviteTTL(v, c.TTL, "ttl")
}
