package request

import (
	"slices"
	"strconv"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/api/problem"
)

// MaxSourceURLBytes caps a source add's url.
const MaxSourceURLBytes = 2048

var (
	detailSourceURLLength = problem.NewDetail("must be an AO3 URL of 1 to " + strconv.Itoa(MaxSourceURLBytes) + " bytes")
	detailSourceURL       = problem.NewDetail("must be a supported AO3 source URL")
	detailOrder           = problem.OneOf(dto.Order(""))
)

// SourceAdd is the body of POST /api/v1/sources. Zero values use the config defaults.
type SourceAdd struct {
	// URL is an AO3 tag, user, pseud, bookmarks, collection, series or work URL.
	URL   string    `json:"url"`
	Order dto.Order `json:"order,omitzero"`
	// Priority orders sources within a scheduling class (higher first).
	Priority     int          `json:"priority,omitzero"`
	PollInterval dto.Duration `json:"poll_interval,omitzero"`
	// Confirm skips the auto_confirm_max gate. Only the admin socket accepts it; that check is
	// the handler's, since it depends on the listener, not the body.
	Confirm bool `json:"confirm,omitzero"`
}

// Validate records every invalid member. The url must parse as a supported AO3 source; each
// ao3url rejection reason has its own detail (the CLI's hints).
func (s *SourceAdd) Validate(v *problem.Validation) {
	switch {
	case s.URL == "" || len(s.URL) > MaxSourceURLBytes:
		v.Member(detailSourceURLLength, "url")
	case !problem.ValidText(s.URL, MaxSourceURLBytes):
		v.Member(problem.DetailText, "url")
	default:
		if _, err := ao3url.Parse(s.URL); err != nil {
			d, ok := problem.URLDetail(err)
			if !ok {
				d = detailSourceURL
			}
			v.Member(d, "url")
		}
	}
	if s.Order != "" && !slices.Contains(s.Order.EnumValues(), string(s.Order)) {
		v.Member(detailOrder, "order")
	}
	validatePriority(v, s.Priority, "priority")
	validatePollInterval(v, s.PollInterval, "poll_interval")
}
