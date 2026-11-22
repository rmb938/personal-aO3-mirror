package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// UserInfo maps an account without its secrets.
func UserInfo(u controllers.UserInfo) dto.User {
	return dto.User{ID: u.ID, Username: dto.Username(u.Username), CreatedAt: utc(u.CreatedAt),
		LastLoginAt: utc(u.LastLoginAt), PasswordChangedAt: utc(u.PasswordChangedAt), DisabledAt: utc(u.DisabledAt),
		FailedAttempts: u.FailedAttempts, RecoveryCodesLeft: u.RecoveryCodesLeft}
}

// UserInfoList maps every account, as the controller ordered them.
func UserInfoList(items []controllers.UserInfo) dto.Items[dto.User] {
	return dto.Items[dto.User]{Items: convSlice(items, UserInfo)}
}
