package request

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
)

func TestSourceSettingsValid(t *testing.T) {
	lo, hi, zero := -MaxSourcePriority, MaxSourcePriority, 0
	for _, s := range []SourceSettings{
		{}, // changes nothing
		{Priority: &zero},
		{Priority: &lo, PollInterval: dto.Duration(MinPollInterval)},
		{Priority: &hi, PollInterval: dto.Duration(MaxPollInterval)},
	} {
		assert.Empty(t, errorsOf(t, &s), "%+v", s)
	}
}

func TestSourceSettingsInvalid(t *testing.T) {
	low, high := -MaxSourcePriority-1, MaxSourcePriority+1
	for name, tc := range map[string]struct {
		in   SourceSettings
		want map[string]string
	}{
		"priority low":   {SourceSettings{Priority: &low}, map[string]string{"/priority": detailPriority.Message()}},
		"priority high":  {SourceSettings{Priority: &high}, map[string]string{"/priority": detailPriority.Message()}},
		"interval short": {SourceSettings{PollInterval: dto.Duration(MinPollInterval - time.Second)}, map[string]string{"/poll_interval": detailPollInterval.Message()}},
		"interval long":  {SourceSettings{PollInterval: dto.Duration(MaxPollInterval + time.Second)}, map[string]string{"/poll_interval": detailPollInterval.Message()}},
		"negative":       {SourceSettings{PollInterval: dto.Duration(-time.Hour)}, map[string]string{"/poll_interval": detailPollInterval.Message()}},
		"both": {SourceSettings{Priority: &high, PollInterval: dto.Duration(time.Second)},
			map[string]string{"/priority": detailPriority.Message(), "/poll_interval": detailPollInterval.Message()}},
	} {
		assert.Equal(t, tc.want, errorsOf(t, &tc.in), name)
	}
}

func TestSourceRefreshValid(t *testing.T) {
	for _, s := range []SourceRefresh{{}, {Full: true}} {
		assert.Empty(t, errorsOf(t, &s), "%+v", s)
	}
}
