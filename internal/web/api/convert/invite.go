package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
)

// Invite maps a signup or reset invite (its token is stored only as a hash, so never shown).
func Invite(i model.Invite) dto.Invite {
	return dto.Invite{ID: i.ID, Kind: dto.InviteKind(i.Kind), UserID: i.TargetUserID, Scope: dto.ResetScope(i.Scope),
		CreatedAt: utc(i.CreatedAt), ExpiresAt: utc(i.ExpiresAt), UsedAt: utc(i.UsedAt), UsedBy: i.UsedBy,
		RevokedAt: utc(i.RevokedAt)}
}

// InviteList maps every invite, as the controller ordered them.
func InviteList(items []model.Invite) dto.Items[dto.Invite] {
	return dto.Items[dto.Invite]{Items: convSlice(items, Invite)}
}

// InviteLink maps a new invite's link.
func InviteLink(l controllers.InviteLink) dto.InviteLink {
	return dto.InviteLink{ID: l.ID, URL: dto.InviteURL(l.URL), ExpiresAt: utc(l.ExpiresAt)}
}

// ResetScope maps a validated reset body's scope to the model's.
func ResetScope(r request.CreateReset) model.ResetScope { return model.ResetScope(r.Scope) }
