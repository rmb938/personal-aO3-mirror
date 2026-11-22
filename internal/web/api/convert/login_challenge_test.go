package convert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestLoginChallenge(t *testing.T) {
	assert.Equal(t, dto.LoginChallenge{ChallengeToken: "chl_fixture", ExpiresAt: t0.Add(5 * time.Minute)},
		LoginChallenge(controllers.LoginChallenge{Token: "chl_fixture", ExpiresAt: t0.Add(5 * time.Minute).In(est)}))
}
