package convert

import (
	"cmp"
	"maps"
	"slices"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Status maps the status report.
func Status(r controllers.StatusReport) dto.Status {
	c := r.Counts
	return dto.Status{
		Daemon: daemon(r.Daemon, r.DaemonRunning),
		Works: dto.WorkCounts{
			ByStatus: counts(c.WorksByStatus, func(k string, n int) dto.WorkStatusCount {
				return dto.WorkStatusCount{Status: dto.WorkStatus(k), Count: n}
			}),
			Fetched: c.WorksFetched, NeverFetched: c.WorksNeverFetch, ParseFailures: c.ParseFailures,
		},
		Tags:     c.Tags,
		Creators: c.Creators,
		Series:   c.Series,
		RawPages: dto.RawPageCounts{Count: c.RawPages, Bytes: c.RawBytes, ZstdBytes: c.RawZstdBytes},
		Requests: dto.RequestCounts{
			Last1h: c.Requests1h, Last24h: c.Requests24h,
			ByClass24h: counts(c.RequestsByClass, func(k string, n int) dto.RequestClassCount {
				return dto.RequestClassCount{Class: dto.RequestClass(k), Count: n}
			}),
		},
		Sources: counts(c.SourcesByState, func(k string, n int) dto.SourceCount {
			return dto.SourceCount{State: dto.SourceState(k), Count: n}
		}),
		Images: dto.ImageCounts{
			ByStatus: counts(r.Images, func(k string, n int) dto.ImageStatusCount {
				return dto.ImageStatusCount{Status: dto.ImageStatus(k), Count: n}
			}),
			Bytes: r.ImageBytes,
		},
		TagInfo: dto.TagInfo{
			Types: mapSlice(r.TagInfo.Types, func(t string) dto.TagInfoType { return dto.TagInfoType(t) }),
			Total: r.TagInfo.Total, Fetched: r.TagInfo.Fetched, Due: r.TagInfo.Due,
		},
		Jobs:          jobCounts(r.Jobs),
		PartialSeries: mapSlice(r.PartialSeries, seriesGap),
		RequestErrors: mapSlice(r.RequestErrors, requestError),
		FailedJobs:    mapSlice(r.FailedJobs, Job),
	}
}

// daemon maps the heartbeat; running is the controller's staleness verdict.
func daemon(h model.Heartbeat, running bool) dto.Daemon {
	return dto.Daemon{
		Running:        running,
		HeartbeatAt:    utc(h.At),
		PID:            h.PID,
		Phase:          dto.DaemonPhase(h.Phase),
		CurrentJob:     h.CurrentJob,
		WaitingUntilAt: utc(h.WaitingUntil),
		Pacer: dto.Pacer{
			IntervalMS:     h.Interval.Milliseconds(),
			SlowFactor:     h.SlowFactor,
			ConsecFails:    h.ConsecFails,
			BreakerOpen:    h.BreakerOpen,
			DayCount:       h.DayCount,
			DailyBudget:    h.DailyBudget,
			BackoffUntilAt: utc(h.BackoffUntil),
		},
	}
}

// counts maps a value → count map to a list sorted by value ([] when empty).
func counts[D any](m map[string]int, conv func(string, int) D) []D {
	out := make([]D, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		out = append(out, conv(k, m[k]))
	}
	return out
}

// jobCounts flattens kind → state → count into a list sorted by kind, then state.
func jobCounts(m map[string]map[string]int) []dto.JobCount {
	out := []dto.JobCount{}
	for kind, states := range m {
		for state, n := range states {
			out = append(out, dto.JobCount{Kind: dto.JobKind(kind), State: dto.JobState(state), Count: n})
		}
	}
	slices.SortFunc(out, func(a, b dto.JobCount) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.State, b.State))
	})
	return out
}

func seriesGap(p store.PartialSeries) dto.SeriesGap {
	return dto.SeriesGap{
		ID: p.ID, WorksTotal: p.WorksTotal, Mirrored: p.Mirrored,
		MissingPositions: nonNil(p.MissingPositions), MissingWorkIDs: nonNil(p.MissingWorkIDs),
	}
}

func requestError(r store.RequestRow) dto.RequestError {
	return dto.RequestError{
		RequestedAt: utc(r.Time), ReqID: r.ReqID, JobID: r.JobID, Kind: dto.RequestKind(r.Kind), Path: r.Path,
		Status: r.Status, Class: dto.RequestClass(r.Class), Reason: r.Reason, Bytes: r.Bytes,
		DurationMS: r.Duration.Milliseconds(), WaitedMS: r.Waited.Milliseconds(), Hop: r.Hop,
	}
}
