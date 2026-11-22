package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// Account maps the signed-in user's own account: no admin bookkeeping (failed attempts,
// disabled time, last login).
func Account(u controllers.UserInfo) dto.Account {
	return dto.Account{ID: u.ID, Username: dto.Username(u.Username), CreatedAt: utc(u.CreatedAt),
		PasswordChangedAt: utc(u.PasswordChangedAt), RecoveryCodesLeft: u.RecoveryCodesLeft}
}
