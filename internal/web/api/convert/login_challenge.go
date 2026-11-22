package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// LoginChallenge maps a login's password step.
func LoginChallenge(c controllers.LoginChallenge) dto.LoginChallenge {
	return dto.LoginChallenge{ChallengeToken: dto.ChallengeToken(c.Token), ExpiresAt: utc(c.ExpiresAt)}
}
