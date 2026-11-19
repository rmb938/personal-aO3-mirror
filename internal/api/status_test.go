package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
)

func TestStatus(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	h := a.Public()

	s := getJSON[dto.Status](t, h, "/api/v1/status")
	d := s.Daemon
	assert.True(t, d.Running, "fresh heartbeat")
	assert.Equal(t, seedTime, d.HeartbeatAt)
	assert.Equal(t, dto.DaemonPhase("running"), d.Phase)
	assert.Equal(t, 42, d.PID)
	assert.Equal(t, seedOtherJob, d.CurrentJob)
	assert.Equal(t, dto.Pacer{IntervalMS: 10000, SlowFactor: 1.5, DayCount: 3, BackoffUntilAt: seedTime.Add(time.Minute)},
		d.Pacer)

	assert.Equal(t, []dto.SourceCount{{State: "active", Count: 1}, {State: "pending_confirm", Count: 1}}, s.Sources)
	assert.Equal(t, []dto.JobCount{
		{Kind: "backfill", State: "queued", Count: 1},
		{Kind: "source_add", State: "done", Count: 1},
		{Kind: "source_add", State: "failed", Count: 1},
		{Kind: "work", State: "queued", Count: 1},
	}, s.Jobs)
	assert.Equal(t, 1, s.Requests.Last1h)
	assert.Equal(t, []dto.RequestClassCount{{Class: "rate_limited", Count: 1}}, s.Requests.ByClass24h)
	assert.Equal(t, []dto.WorkStatusCount{}, s.Works.ByStatus, "empty lists are [], not null")
	assert.Equal(t, []dto.ImageStatusCount{}, s.Images.ByStatus)
	assert.Equal(t, []dto.TagInfoType{}, s.TagInfo.Types, "tag info off")
	assert.Equal(t, []dto.SeriesGap{}, s.PartialSeries)

	require.Len(t, s.RequestErrors, 1)
	re := s.RequestErrors[0]
	assert.Equal(t, dto.RequestKind("listing"), re.Kind)
	assert.Equal(t, dto.RequestClass("rate_limited"), re.Class)
	assert.Equal(t, 429, re.Status)
	assert.Equal(t, seedBackfillJob, re.JobID)
	assert.Equal(t, int64(1000), re.DurationMS)
	assert.Equal(t, seedTime, re.RequestedAt)
	assert.True(t, isHash(re.ReqID) && isHash(re.Path) && isHash(re.Reason), "request text hashed")

	require.Len(t, s.FailedJobs, 1)
	assert.Equal(t, seedFailedAdd, s.FailedJobs[0].ID)
	assert.True(t, isHash(s.FailedJobs[0].LastErrorDetail), "failed job detail hashed")

	w := get(h, "/api/v1/status?verbose=1")
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, dto.CodeUnknownParam, decodeProblem(t, w).Code)
}
