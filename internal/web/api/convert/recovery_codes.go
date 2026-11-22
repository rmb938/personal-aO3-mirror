package convert

import "github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"

// RecoveryCodes maps newly issued recovery codes, shown only in this response: always a list,
// empty for none.
func RecoveryCodes(codes []string) dto.RecoveryCodes {
	out := make([]dto.RecoveryCode, len(codes))
	for i, c := range codes {
		out[i] = dto.RecoveryCode(c)
	}
	return dto.RecoveryCodes{RecoveryCodes: out}
}
