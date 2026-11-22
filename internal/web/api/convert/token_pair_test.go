package convert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestTokenPair(t *testing.T) {
	assert.Equal(t,
		dto.TokenPair{AccessToken: "eyJfixture", TokenType: "Bearer", ExpiresIn: 900, RefreshToken: "rt_fixture",
			RefreshExpiresAt: t0.Add(720 * time.Hour)},
		TokenPair(controllers.TokenPair{UserID: 1, SessionID: 2, AccessToken: "eyJfixture",
			AccessExpiresAt: t0.Add(15 * time.Minute), AccessTTL: 15 * time.Minute, RefreshToken: "rt_fixture",
			RefreshExpiresAt: t0.Add(720 * time.Hour).In(est)}))
}

func TestCeilSeconds(t *testing.T) {
	for d, want := range map[time.Duration]int64{0: 0, time.Nanosecond: 1, time.Second: 1, 1500 * time.Millisecond: 2,
		15 * time.Minute: 900} {
		assert.Equal(t, want, ceilSeconds(d), d)
	}
}
