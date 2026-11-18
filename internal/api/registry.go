package api

import (
	"maps"
	"slices"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/api/redact"
)

// responseTypes are every DTO the API returns. Each route's response type must be listed, or
// the enforcer rejects its keys (500).
var responseTypes = []any{
	dto.Problem{},
	dto.Health{},
	dto.Version{},
}

// healthOK is the only Health.Status value.
const healthOK = "ok"

// newEnforcer builds the response enforcer from the DTO types and the API's closed value sets.
// version is the running build version, registered as the only `version` value.
func newEnforcer(h *redact.Hasher, version string) (*redact.Enforcer, error) {
	reg := redact.NewRegistry()
	if err := reg.RegisterTypes(responseTypes...); err != nil {
		return nil, err
	}
	codes := make([]string, 0, len(codeInfo))
	titles := make([]string, 0, len(codeInfo))
	for c, info := range codeInfo {
		codes = append(codes, string(c))
		titles = append(titles, info.title)
	}
	enums := map[string][]string{
		"code":    codes,
		"title":   titles,
		"field":   slices.Collect(maps.Keys(knownFields)),
		"status":  {healthOK},
		"version": {version},
	}
	for key, vals := range enums {
		if err := reg.RegisterEnum(key, vals...); err != nil {
			return nil, err
		}
	}
	return redact.NewEnforcer(reg, h)
}
