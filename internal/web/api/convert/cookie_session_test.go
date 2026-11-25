package convert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestCookieSession(t *testing.T) {
	assert.Equal(t,
		dto.CookieSession{Transport: "cookie", ExpiresIn: 900, RefreshExpiresAt: t0.Add(720 * time.Hour)},
		CookieSession(controllers.TokenPair{UserID: 1, SessionID: 2, AccessToken: "eyJfixture",
			AccessExpiresAt: t0.Add(15 * time.Minute), AccessTTL: 15 * time.Minute, RefreshToken: "rt_fixture",
			RefreshExpiresAt: t0.Add(720 * time.Hour).In(est)}))
}
