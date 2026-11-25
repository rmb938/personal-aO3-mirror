package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// CookieSession maps a session's new credentials for the cookie transport: only the expiry
// times, since the tokens go in cookies (runtime.SessionCookies), never the body.
func CookieSession(p controllers.TokenPair) dto.CookieSession {
	return dto.CookieSession{Transport: dto.TransportCookie, ExpiresIn: ceilSeconds(p.AccessTTL),
		RefreshExpiresAt: utc(p.RefreshExpiresAt)}
}
