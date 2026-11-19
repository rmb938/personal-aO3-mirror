package api

import (
	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/api/redact"
)

// responseTypes are every DTO the API returns. Each route's response type must be listed, or
// the enforcer refuses its responses (500). Each body is checked against its own type's schema;
// enum values come from the DTOs' typed fields (redact.Enum), per field.
var responseTypes = []any{
	dto.Problem{},
	dto.Health{},
	dto.Version{},
	dto.List[dto.Source]{},
	dto.Source{},
	dto.SourceAdd{},
	dto.List[dto.Job]{},
	dto.Job{},
	dto.Status{},
}

// newEnforcer builds the response enforcer from the DTO types. version is the running build
// version, registered as the only dto.Version.Version value (it is known only at runtime).
func newEnforcer(h *redact.Hasher, version string) (*redact.Enforcer, error) {
	reg := redact.NewRegistry()
	if err := reg.RegisterTypes(responseTypes...); err != nil {
		return nil, err
	}
	if err := reg.RegisterFieldEnum(dto.Version{}, "version", version); err != nil {
		return nil, err
	}
	return redact.NewEnforcer(reg, h)
}
