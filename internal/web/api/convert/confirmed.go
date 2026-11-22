package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// Confirmed maps a completed signup or reset to its recovery codes: always a list, empty when
// a password-only reset keeps the current codes.
func Confirmed(c controllers.Confirmed) dto.RecoveryCodes { return RecoveryCodes(c.RecoveryCodes) }
