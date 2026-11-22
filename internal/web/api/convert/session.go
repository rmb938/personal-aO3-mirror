package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// Session maps an active session.
func Session(s model.Session) dto.Session {
	return dto.Session{ID: s.ID, CreatedAt: utc(s.CreatedAt), LastUsedAt: utc(s.LastUsedAt), ExpiresAt: utc(s.ExpiresAt)}
}

// SessionList maps a user's sessions, as the controller ordered them.
func SessionList(items []model.Session) dto.Items[dto.Session] {
	return dto.Items[dto.Session]{Items: convSlice(items, Session)}
}
