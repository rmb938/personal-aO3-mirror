package convert

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
)

var (
	t0 = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	// est is t0 in another zone: converters write UTC.
	est = time.FixedZone("EST", -5*3600)
)

func TestInvite(t *testing.T) {
	assert.Equal(t, dto.Invite{ID: 1, Kind: "signup", CreatedAt: t0, ExpiresAt: t0.Add(time.Hour)},
		Invite(model.Invite{ID: 1, Kind: model.InviteSignup, CreatedAt: t0.In(est), ExpiresAt: t0.Add(time.Hour)}))
	assert.Equal(t,
		dto.Invite{ID: 2, Kind: "reset", UserID: 3, Scope: "both", CreatedAt: t0, ExpiresAt: t0.Add(time.Hour),
			UsedAt: t0.Add(time.Minute), UsedBy: 3, RevokedAt: t0.Add(2 * time.Minute)},
		Invite(model.Invite{ID: 2, Kind: model.InviteReset, TargetUserID: 3, Scope: model.ResetBoth, CreatedAt: t0,
			ExpiresAt: t0.Add(time.Hour), UsedAt: t0.Add(time.Minute).In(est), UsedBy: 3, RevokedAt: t0.Add(2 * time.Minute)}))
}

func TestInviteList(t *testing.T) {
	assert.Equal(t, dto.Items[dto.Invite]{}, InviteList(nil))
	assert.Equal(t, dto.Items[dto.Invite]{Items: []dto.Invite{{ID: 2, Kind: "signup"}, {ID: 1, Kind: "signup"}}},
		InviteList([]model.Invite{{ID: 2, Kind: model.InviteSignup}, {ID: 1, Kind: model.InviteSignup}}))
}

func TestInviteLink(t *testing.T) {
	assert.Equal(t, dto.InviteLink{ID: 4, URL: "https://mirror.example.com/signup#token=x", ExpiresAt: t0},
		InviteLink(controllers.InviteLink{ID: 4, URL: "https://mirror.example.com/signup#token=x", ExpiresAt: t0.In(est)}))
}

func TestResetScope(t *testing.T) {
	for _, s := range model.ResetScopes() {
		assert.Equal(t, s, ResetScope(request.CreateReset{Scope: dto.ResetScope(s)}))
	}
}

// The enums and bounds the API mirrors from the model and controllers.
func TestAccountEnumsMatchModel(t *testing.T) {
	scopes := make([]string, 0, len(model.ResetScopes()))
	for _, s := range model.ResetScopes() {
		scopes = append(scopes, string(s))
	}
	assert.ElementsMatch(t, scopes, dto.ResetScope("").EnumValues())
	kinds := dto.InviteKind("").EnumValues()
	assert.True(t, slices.Contains(kinds, string(model.InviteSignup)) && slices.Contains(kinds, string(model.InviteReset)))
	assert.Len(t, kinds, 2)
	assert.Equal(t, controllers.MinInviteTTL, request.MinInviteTTL)
	assert.Equal(t, controllers.MaxInviteTTL, request.MaxInviteTTL)
}
