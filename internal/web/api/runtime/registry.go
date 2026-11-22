package runtime

import (
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/redact"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
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
	dto.SourceAddResult{},
	dto.SourceAddQueued{},
	dto.SourceCancelled{},
	dto.SourceRefreshed{},
	dto.List[dto.Job]{},
	dto.Job{},
	dto.Status{},
	dto.Work{},
	dto.List[dto.WorkBlurb]{},
	dto.WorkSkin{},
	dto.Chapter{},
	dto.WorkChapters{},
	dto.Tag{},
	dto.List[dto.TagListItem]{},
	dto.List[dto.Medium]{},
	dto.Series{},
	dto.Creator{},
	dto.List[dto.Creator]{},
	dto.Collection{},
	dto.JobDebug{},
	dto.WorkDebug{},
	dto.Items[dto.Invite]{},
	dto.InviteLink{},
	dto.Items[dto.User]{},
	dto.Items[dto.Session]{},
	dto.Enrollment{},
	dto.RecoveryCodes{},
	dto.LoginChallenge{},
	dto.TokenPair{},
	dto.Account{},
}

// requestTypes are every request body type. A validation error's errors[].pointer passes only
// when it names a member of one of them (problem.Validation.Member builds it from their json
// keys).
var requestTypes = request.Types()

// newEnforcer builds the response enforcer from the DTO types. version is the running build
// version, registered as the only dto.Version.Version value (it is known only at runtime).
// Problem details are the fixed catalogue (problem.Catalogue, which details.go adds to).
func NewEnforcer(h *redact.Hasher, version string) (*redact.Enforcer, error) {
	reg := redact.NewRegistry()
	if err := reg.RegisterTypes(responseTypes...); err != nil {
		return nil, err
	}
	if err := reg.RegisterFieldEnum(dto.Version{}, "version", version); err != nil {
		return nil, err
	}
	cat := problem.Catalogue()
	if err := reg.RegisterFieldEnum(dto.Problem{}, "detail", cat...); err != nil {
		return nil, err
	}
	if err := reg.RegisterFieldEnum(dto.ProblemError{}, "detail", cat...); err != nil {
		return nil, err
	}
	if err := reg.RegisterPointerField(dto.ProblemError{}, "pointer", requestTypes...); err != nil {
		return nil, err
	}
	return redact.NewEnforcer(reg, h)
}
