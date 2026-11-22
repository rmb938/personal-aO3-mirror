package convert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestEnrollment(t *testing.T) {
	assert.Equal(t,
		dto.Enrollment{EnrollmentToken: "enr_fixture", TOTPSecret: "FIXTURE", OTPAuthURI: "otpauth://fixture",
			ExpiresAt: t0.Add(15 * time.Minute)},
		Enrollment(controllers.Enrollment{Token: "enr_fixture", TOTPSecret: "FIXTURE", OTPAuthURI: "otpauth://fixture",
			ExpiresAt: t0.Add(15 * time.Minute).In(est)}))
	assert.Equal(t, dto.Enrollment{EnrollmentToken: "enr_fixture", ExpiresAt: t0},
		Enrollment(controllers.Enrollment{Token: "enr_fixture", ExpiresAt: t0}))
}
