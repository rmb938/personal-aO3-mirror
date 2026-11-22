package convert

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestConfirmed(t *testing.T) {
	assert.Equal(t, dto.RecoveryCodes{RecoveryCodes: []dto.RecoveryCode{"aaaa-bbbb-cccc-dddd", "eeee-ffff-gggg-hhhh"}},
		Confirmed(controllers.Confirmed{UserID: 1, RecoveryCodes: []string{"aaaa-bbbb-cccc-dddd", "eeee-ffff-gggg-hhhh"}}))
	// A password-only reset: an empty list, not null.
	assert.Equal(t, dto.RecoveryCodes{RecoveryCodes: []dto.RecoveryCode{}}, Confirmed(controllers.Confirmed{UserID: 1}))
}
