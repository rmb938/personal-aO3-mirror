package convert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

func TestStatus(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	r := controllers.StatusReport{
		Counts: store.Counts{
			WorksByStatus: map[string]int{"ok": 5, "deleted": 1}, WorksFetched: 4, WorksNeverFetch: 1, ParseFailures: 2,
			Tags: 7, Creators: 3, Series: 1, RawPages: 4, RawBytes: 4000, RawZstdBytes: 900,
			Requests1h: 2, Requests24h: 9, RequestsByClass: map[string]int{"ok": 8, "unavailable": 1},
			SourcesByState: map[string]int{"active": 2},
		},
		Daemon:        model.Heartbeat{At: at, PID: 1, Phase: model.DaemonIdle},
		DaemonRunning: true,
		Images:        map[string]int{"queued": 3},
		ImageBytes:    512,
		TagInfo:       controllers.TagInfoProgress{Types: []string{"Fandom"}, Total: 4, Fetched: 3, Due: 1},
		Jobs:          map[string]map[string]int{"work": {"queued": 2, "done": 1}, "poll": {"done": 1}},
		PartialSeries: []store.PartialSeries{{ID: 8, WorksTotal: 3, Mirrored: 1, MissingPositions: []int{2, 3}}},
		RequestErrors: []store.RequestRow{{Time: at, ReqID: "r1", Kind: "work", Path: "/works/1", Status: 503,
			Class: "unavailable", Reason: "status=503", Duration: 1500 * time.Millisecond, Waited: 2 * time.Second, Hop: 1}},
		FailedJobs: []model.Job{{ID: 3, Kind: "work", State: model.JobFailed, LastError: "not_found"}},
	}
	got := Status(r)
	assert.Equal(t, dto.WorkCounts{
		ByStatus: []dto.WorkStatusCount{{Status: "deleted", Count: 1}, {Status: "ok", Count: 5}},
		Fetched:  4, NeverFetched: 1, ParseFailures: 2,
	}, got.Works)
	assert.Equal(t, [3]int{7, 3, 1}, [3]int{got.Tags, got.Creators, got.Series})
	assert.Equal(t, dto.RawPageCounts{Count: 4, Bytes: 4000, ZstdBytes: 900}, got.RawPages)
	assert.Equal(t, dto.RequestCounts{Last1h: 2, Last24h: 9, ByClass24h: []dto.RequestClassCount{
		{Class: "ok", Count: 8}, {Class: "unavailable", Count: 1}}}, got.Requests)
	assert.Equal(t, []dto.SourceCount{{State: "active", Count: 2}}, got.Sources)
	assert.Equal(t, dto.ImageCounts{ByStatus: []dto.ImageStatusCount{{Status: "queued", Count: 3}}, Bytes: 512}, got.Images)
	assert.Equal(t, dto.TagInfo{Types: []dto.TagInfoType{"Fandom"}, Total: 4, Fetched: 3, Due: 1}, got.TagInfo)
	assert.Equal(t, []dto.JobCount{
		{Kind: "poll", State: "done", Count: 1}, {Kind: "work", State: "done", Count: 1}, {Kind: "work", State: "queued", Count: 2},
	}, got.Jobs, "sorted by kind, then state")
	assert.Equal(t, []dto.SeriesGap{{ID: 8, WorksTotal: 3, Mirrored: 1, MissingPositions: []int{2, 3},
		MissingWorkIDs: []int64{}}}, got.PartialSeries)
	assert.Equal(t, []dto.RequestError{{RequestedAt: at.UTC(), ReqID: "r1", Kind: "work", Path: "/works/1", Status: 503,
		Class: "unavailable", Reason: "status=503", DurationMS: 1500, WaitedMS: 2000, Hop: 1}}, got.RequestErrors)
	assert.Equal(t, time.UTC, got.RequestErrors[0].RequestedAt.Location())
	assert.Equal(t, []dto.Job{{ID: 3, Kind: "work", State: "failed", LastErrorCode: "not_found"}}, got.FailedJobs)
	assert.True(t, got.Daemon.Running)

	empty := Status(controllers.StatusReport{})
	assert.Equal(t, []dto.WorkStatusCount{}, empty.Works.ByStatus, "nil maps give [], not null")
	assert.Equal(t, []dto.JobCount{}, empty.Jobs)
	assert.Equal(t, []dto.TagInfoType{}, empty.TagInfo.Types)
	assert.Equal(t, []dto.RequestError{}, empty.RequestErrors)
	assert.Equal(t, []dto.Job{}, empty.FailedJobs)
}

func TestDaemon(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	h := model.Heartbeat{At: at, PID: 9, Phase: model.DaemonRunning, CurrentJob: 4, WaitingUntil: at.Add(time.Second),
		Interval: 12500 * time.Millisecond, SlowFactor: 2, ConsecFails: 3, BreakerOpen: true, DayCount: 50,
		DailyBudget: 1000, BackoffUntil: at.Add(time.Minute)}
	assert.Equal(t, dto.Daemon{Running: false, HeartbeatAt: at.UTC(), PID: 9, Phase: "running", CurrentJob: 4,
		WaitingUntilAt: at.Add(time.Second).UTC(), Pacer: dto.Pacer{IntervalMS: 12500, SlowFactor: 2, ConsecFails: 3,
			BreakerOpen: true, DayCount: 50, DailyBudget: 1000, BackoffUntilAt: at.Add(time.Minute).UTC()}}, daemon(h, false))

	z := daemon(model.Heartbeat{}, false)
	assert.True(t, z.HeartbeatAt.IsZero() && z.WaitingUntilAt.IsZero() && z.Pacer.BackoffUntilAt.IsZero(),
		"a daemon that never ran has no times")
	assert.Empty(t, z.Phase)
}

func TestCounts(t *testing.T) {
	conv := func(k string, n int) dto.SourceCount { return dto.SourceCount{State: dto.SourceState(k), Count: n} }
	assert.Equal(t, []dto.SourceCount{}, counts(nil, conv))
	assert.Equal(t, []dto.SourceCount{{State: "a", Count: 2}, {State: "b", Count: 1}},
		counts(map[string]int{"b": 1, "a": 2}, conv), "sorted by value")
}

func TestJobCounts(t *testing.T) {
	assert.Equal(t, []dto.JobCount{}, jobCounts(nil))
	assert.Equal(t, []dto.JobCount{{Kind: "a", State: "x", Count: 1}, {Kind: "a", State: "y", Count: 2}, {Kind: "b", State: "x", Count: 3}},
		jobCounts(map[string]map[string]int{"b": {"x": 3}, "a": {"y": 2, "x": 1}}))
}

func TestSeriesGap(t *testing.T) {
	assert.Equal(t, dto.SeriesGap{ID: 1, WorksTotal: 2, Mirrored: 1, MissingPositions: []int{}, MissingWorkIDs: []int64{5}},
		seriesGap(store.PartialSeries{ID: 1, WorksTotal: 2, Mirrored: 1, MissingWorkIDs: []int64{5}}))
}

func TestRequestError(t *testing.T) {
	got := requestError(store.RequestRow{Kind: "robots", Class: "ok", Status: 200})
	assert.Equal(t, dto.RequestError{Kind: "robots", Class: "ok", Status: 200}, got)
	assert.Zero(t, got.JobID, "no job: omitted")
}
