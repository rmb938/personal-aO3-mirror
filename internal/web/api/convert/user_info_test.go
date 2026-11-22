package convert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestUserInfo(t *testing.T) {
	assert.Equal(t, dto.User{ID: 1, Username: "fixture_user", CreatedAt: t0, RecoveryCodesLeft: 10},
		UserInfo(controllers.UserInfo{ID: 1, Username: "fixture_user", CreatedAt: t0.In(est), RecoveryCodesLeft: 10}))
	assert.Equal(t,
		dto.User{ID: 2, Username: "fixture_two", CreatedAt: t0, LastLoginAt: t0.Add(time.Hour),
			PasswordChangedAt: t0.Add(2 * time.Hour), DisabledAt: t0.Add(3 * time.Hour), FailedAttempts: 4, RecoveryCodesLeft: 9},
		UserInfo(controllers.UserInfo{ID: 2, Username: "fixture_two", CreatedAt: t0, LastLoginAt: t0.Add(time.Hour).In(est),
			PasswordChangedAt: t0.Add(2 * time.Hour), DisabledAt: t0.Add(3 * time.Hour).In(est), FailedAttempts: 4, RecoveryCodesLeft: 9}))
}

func TestUserInfoList(t *testing.T) {
	assert.Equal(t, dto.Items[dto.User]{}, UserInfoList(nil))
	assert.Equal(t, dto.Items[dto.User]{Items: []dto.User{{ID: 1, Username: "fixture_user"}}},
		UserInfoList([]controllers.UserInfo{{ID: 1, Username: "fixture_user"}}))
}
