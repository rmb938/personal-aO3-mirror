package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// Session maps an active session; current is the ID of the caller's own session (0 when the
// caller has none, as on the admin socket).
func Session(s model.Session, current int64) dto.Session {
	return dto.Session{ID: s.ID, CreatedAt: utc(s.CreatedAt), LastUsedAt: utc(s.LastUsedAt), ExpiresAt: utc(s.ExpiresAt),
		Current: current != 0 && s.ID == current}
}

// SessionList maps a user's sessions, as the controller ordered them, marking the caller's own
// session (current, 0 for none).
func SessionList(items []model.Session, current int64) dto.Items[dto.Session] {
	return dto.Items[dto.Session]{Items: convSlice(items, func(s model.Session) dto.Session { return Session(s, current) })}
}
