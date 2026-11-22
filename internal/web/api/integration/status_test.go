package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

func TestStatus(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := signedIn(t, a)

	s := getJSON[dto.Status](t, h, "/api/v1/status")
	d := s.Daemon
	assert.True(t, d.Running, "fresh heartbeat")
	assert.Equal(t, seedTime, d.HeartbeatAt)
	assert.Equal(t, dto.DaemonPhase("running"), d.Phase)
	assert.Equal(t, 42, d.PID)
	assert.Equal(t, seedOtherJob, d.CurrentJob)
	assert.Equal(t, dto.Pacer{Interval: dto.Duration(10 * time.Second), SlowFactor: 1.5, DayCount: 3, BackoffUntilAt: seedTime.Add(time.Minute)},
		d.Pacer)

	assert.Equal(t, []dto.SourceCount{{State: "active", Count: 1}, {State: "pending_confirm", Count: 1}}, s.Sources)
	assert.Equal(t, []dto.JobCount{
		{Kind: "backfill", State: "queued", Count: 1},
		{Kind: "source_add", State: "done", Count: 1},
		{Kind: "source_add", State: "failed", Count: 1},
		{Kind: "work", State: "queued", Count: 1},
	}, s.Jobs)
	assert.Equal(t, 2, s.Requests.Last1h)
	assert.Equal(t, []dto.RequestClassCount{{Class: "ok", Count: 1}, {Class: "rate_limited", Count: 1}}, s.Requests.ByClass24h)
	assert.Equal(t, []dto.WorkStatusCount{{Status: "ok", Count: 2}}, s.Works.ByStatus)
	assert.Equal(t, []dto.ImageStatusCount{{Status: "ok", Count: 1}, {Status: "queued", Count: 1}}, s.Images.ByStatus)
	assert.Equal(t, []dto.TagInfoType{}, s.TagInfo.Types,
		"tag info off; empty lists are [], not null (json/v2 writes a nil slice as [])")
	assert.Equal(t, []dto.SeriesGap{{ID: seedSeries, WorksTotal: 3, Mirrored: 1, MissingPositions: []int{1, 3},
		MissingWorkIDs: []int64{seedSeriesOther}}}, s.PartialSeries)

	require.Len(t, s.RequestErrors, 1)
	re := s.RequestErrors[0]
	assert.Equal(t, dto.RequestKind("listing"), re.Kind)
	assert.Equal(t, dto.RequestClass("rate_limited"), re.Class)
	assert.Equal(t, 429, re.Status)
	assert.Equal(t, seedBackfillJob, re.JobID)
	assert.Equal(t, dto.Duration(time.Second), re.Duration)
	assert.Equal(t, seedTime, re.RequestedAt)
	assert.True(t, isHash(t, re.ReqID) && isHash(t, re.Path) && isHash(t, re.Reason), "request text hashed")

	require.Len(t, s.FailedJobs, 1)
	assert.Equal(t, seedFailedAdd, s.FailedJobs[0].ID)
	assert.True(t, isHash(t, s.FailedJobs[0].LastErrorDetail), "failed job detail hashed")

	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/status?verbose=1"), "", "unknown param")
}
