package request

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestCreateInviteValid(t *testing.T) {
	for _, c := range []CreateInvite{{}, {TTL: dto.Duration(MinInviteTTL)}, {TTL: dto.Duration(MaxInviteTTL)}} {
		assert.Empty(t, errorsOf(t, &c), "%+v", c)
	}
}

func TestCreateInviteInvalid(t *testing.T) {
	want := map[string]string{"/ttl": detailInviteTTL.Message()}
	for _, d := range []time.Duration{MinInviteTTL - time.Second, MaxInviteTTL + time.Second, -time.Hour} {
		assert.Equal(t, want, errorsOf(t, &CreateInvite{TTL: dto.Duration(d)}), d)
	}
}

func TestCreateResetValid(t *testing.T) {
	for _, scope := range dto.ResetScope("").EnumValues() {
		for _, c := range []CreateReset{{Scope: dto.ResetScope(scope)}, {Scope: dto.ResetScope(scope), TTL: dto.Duration(time.Hour)}} {
			assert.Empty(t, errorsOf(t, &c), "%+v", c)
		}
	}
}

func TestCreateResetInvalid(t *testing.T) {
	for name, tc := range map[string]struct {
		in   CreateReset
		want map[string]string
	}{
		"no scope":      {CreateReset{}, map[string]string{"/scope": detailResetScope.Message()}},
		"unknown scope": {CreateReset{Scope: "Password"}, map[string]string{"/scope": detailResetScope.Message()}},
		"both": {CreateReset{Scope: "all", TTL: dto.Duration(MaxInviteTTL + 1)},
			map[string]string{"/scope": detailResetScope.Message(), "/ttl": detailInviteTTL.Message()}},
	} {
		assert.Equal(t, tc.want, errorsOf(t, &tc.in), name)
	}
}
