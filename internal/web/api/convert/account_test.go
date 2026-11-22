package convert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestAccount(t *testing.T) {
	assert.Equal(t, dto.Account{ID: 1, Username: "fixture_user", CreatedAt: t0, RecoveryCodesLeft: 10},
		Account(controllers.UserInfo{ID: 1, Username: "fixture_user", CreatedAt: t0.In(est), RecoveryCodesLeft: 10}))
	// The admin's bookkeeping is left out.
	assert.Equal(t,
		dto.Account{ID: 2, Username: "fixture_two", CreatedAt: t0, PasswordChangedAt: t0.Add(2 * time.Hour), RecoveryCodesLeft: 9},
		Account(controllers.UserInfo{ID: 2, Username: "fixture_two", CreatedAt: t0, LastLoginAt: t0.Add(time.Hour),
			PasswordChangedAt: t0.Add(2 * time.Hour).In(est), DisabledAt: t0.Add(3 * time.Hour), FailedAttempts: 4,
			RecoveryCodesLeft: 9}))
}
