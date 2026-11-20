package request

import (
	"strconv"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
)

// Bounds shared by the source add and source settings bodies.
const (
	// MaxSourcePriority bounds a source's priority (both signs).
	MaxSourcePriority = 1000
	// MinPollInterval and MaxPollInterval bound a source's poll_interval (10m, as config
	// sources.poll_interval, to 30 days).
	MinPollInterval = 10 * time.Minute
	MaxPollInterval = 30 * 24 * time.Hour
)

var (
	detailPriority = problem.NewDetail("must be an integer from -" + strconv.Itoa(MaxSourcePriority) +
		" to " + strconv.Itoa(MaxSourcePriority))
	detailPollInterval = problem.NewDetail("must be omitted or a duration from " +
		MinPollInterval.String() + " to " + MaxPollInterval.String())
)

// validatePriority records p at key when it is out of range.
func validatePriority(v *problem.Validation, p int, key string) {
	if p < -MaxSourcePriority || p > MaxSourcePriority {
		v.Member(detailPriority, key)
	}
}

// validatePollInterval records d at key when it is set (non-zero) and out of range.
func validatePollInterval(v *problem.Validation, d dto.Duration, key string) {
	if p := d.Duration(); p != 0 && (p < MinPollInterval || p > MaxPollInterval) {
		v.Member(detailPollInterval, key)
	}
}
