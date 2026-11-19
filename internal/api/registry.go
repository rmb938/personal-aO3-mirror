package api

import (
	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/api/redact"
)

// responseTypes are every DTO the API returns. Each route's response type must be listed, or
// the enforcer rejects its keys (500). Enum keys come from the DTOs' typed fields (redact.Enum).
var responseTypes = []any{
	dto.Problem{},
	dto.Health{},
	dto.Version{},
	dto.List[dto.Source]{},
	dto.Source{},
	dto.SourceAdd{},
	dto.List[dto.Job]{},
	dto.Job{},
}

// newEnforcer builds the response enforcer from the DTO types. version is the running build
// version, registered as the only `version` value (it is known only at runtime).
func newEnforcer(h *redact.Hasher, version string) (*redact.Enforcer, error) {
	reg := redact.NewRegistry()
	if err := reg.RegisterTypes(responseTypes...); err != nil {
		return nil, err
	}
	if err := reg.RegisterEnum("version", version); err != nil {
		return nil, err
	}
	return redact.NewEnforcer(reg, h)
}
