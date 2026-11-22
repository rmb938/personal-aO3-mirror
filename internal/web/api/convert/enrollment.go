package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// Enrollment maps a signup's or reset's first step (the TOTP fields are empty when a reset
// keeps the current secret).
func Enrollment(e controllers.Enrollment) dto.Enrollment {
	return dto.Enrollment{EnrollmentToken: dto.EnrollmentToken(e.Token), TOTPSecret: dto.TOTPSecret(e.TOTPSecret),
		OTPAuthURI: dto.OTPAuthURI(e.OTPAuthURI), ExpiresAt: utc(e.ExpiresAt)}
}
