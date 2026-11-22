package convert

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestRecoveryCodes(t *testing.T) {
	assert.Equal(t, dto.RecoveryCodes{RecoveryCodes: []dto.RecoveryCode{"aaaa-bbbb-cccc-dddd", "eeee-ffff-gggg-hhhh"}},
		RecoveryCodes([]string{"aaaa-bbbb-cccc-dddd", "eeee-ffff-gggg-hhhh"}))
	assert.Equal(t, dto.RecoveryCodes{RecoveryCodes: []dto.RecoveryCode{}}, RecoveryCodes(nil), "an empty list, not null")
}
