package request

import (
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
)

// SourceSettings is the body of PATCH /api/v1/sources/{id} (admin socket). An absent member
// keeps the source's current value, so `{}` changes nothing.
type SourceSettings struct {
	// PollInterval is the update poll interval; absent (or zero) keeps the current one.
	PollInterval dto.Duration `json:"poll_interval,omitzero"`
	// Priority orders sources within a scheduling class (higher first); absent (or null)
	// keeps the current one.
	Priority *int `json:"priority,omitzero"`
}

// Validate records every out-of-range member.
func (s *SourceSettings) Validate(v *problem.Validation) {
	if s.Priority != nil {
		validatePriority(v, *s.Priority, "priority")
	}
	validatePollInterval(v, s.PollInterval, "poll_interval")
}
