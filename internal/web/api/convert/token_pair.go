package convert

import (
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// TokenPair maps a session's new credentials. expires_in is the access TTL in whole seconds
// (rounded up, so a client never waits past the token's expiry).
func TokenPair(p controllers.TokenPair) dto.TokenPair {
	return dto.TokenPair{AccessToken: dto.AccessToken(p.AccessToken), TokenType: dto.TokenTypeBearer,
		ExpiresIn: ceilSeconds(p.AccessTTL), RefreshToken: dto.RefreshToken(p.RefreshToken),
		RefreshExpiresAt: utc(p.RefreshExpiresAt)}
}

// ceilSeconds is d in whole seconds, rounded up.
func ceilSeconds(d time.Duration) int64 { return int64((d + time.Second - 1) / time.Second) }
