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

func TestJobDebug(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	got := JobDebug(controllers.JobDebug{
		Job: model.Job{ID: 4, Kind: "work", Target: "7", State: model.JobDone, DedupeKey: "work:7"},
		Events: []controllers.JobEvent{{Time: at, Event: model.JobEventStart, Outcome: controllers.JobOutcome{Attempt: new(1)},
			Detail: &controllers.WorkDetail{}}},
		Requests: []store.RequestRow{{Time: at, Kind: "work", Path: "/works/7", Status: 200, Class: "ok"}},
	})
	assert.Equal(t, dto.JobDebug{
		Job:       dto.Job{ID: 4, Kind: "work", Target: "7", State: "done"},
		DedupeKey: "work:7",
		Events: []dto.JobEvent{{OccurredAt: at.UTC(), Event: "start", Outcome: dto.JobOutcome{Attempt: new(1)},
			Work: &dto.WorkDetail{}}},
		Requests: []dto.Request{{RequestedAt: at.UTC(), Kind: "work", Path: "/works/7", Status: 200, Class: "ok"}},
	}, got)
	assert.Equal(t, time.UTC, got.Events[0].OccurredAt.Location())

	empty := JobDebug(controllers.JobDebug{})
	assert.Equal(t, []dto.JobEvent{}, empty.Events, "nil gives [], not null")
	assert.Equal(t, []dto.Request{}, empty.Requests)
}

func TestWorkDebug(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	got := WorkDebug(controllers.WorkDebug{
		ID:       7,
		Diag:     store.WorkDiag{Status: model.StatusOK, Published: "2025-01-02", Words: 1200, ChaptersPosted: 2},
		Jobs:     []model.Job{{ID: 3, Kind: "work", State: model.JobQueued}},
		Requests: []store.RequestRow{{Time: at, Kind: "work", Path: "/works/7", Status: 200, Class: "ok"}},
	})
	assert.Equal(t, int64(7), got.Work.ID)
	assert.Equal(t, []dto.Job{{ID: 3, Kind: "work", State: "queued"}}, got.Jobs)
	assert.Equal(t, []dto.Request{{RequestedAt: at.UTC(), Kind: "work", Path: "/works/7", Status: 200, Class: "ok"}}, got.Requests)

	empty := WorkDebug(controllers.WorkDebug{})
	assert.Equal(t, []dto.Job{}, empty.Jobs, "nil gives [], not null")
	assert.Equal(t, []dto.Request{}, empty.Requests)
}

// Each kind's detail lands in the member named after the kind.
func TestJobEvent(t *testing.T) {
	ev := func(d controllers.JobDetail) dto.JobEvent {
		return jobEvent(controllers.JobEvent{Event: model.JobEventDone, Detail: d})
	}
	assert.Equal(t, &dto.PollDetail{Page: new(1)}, ev(&controllers.PollDetail{Page: new(1)}).Poll)
	assert.Equal(t, &dto.BackfillDetail{Page: new(2)}, ev(&controllers.BackfillDetail{Page: new(2)}).Backfill)
	assert.Equal(t, &dto.WorkDetail{Words: new(3)}, ev(&controllers.WorkDetail{Words: new(3)}).Work)
	assert.Equal(t, &dto.TagInfoDetail{Parents: new(4)}, ev(&controllers.TagInfoDetail{Parents: new(4)}).TagInfo)
	assert.Equal(t, &dto.SeriesInfoDetail{MembersSeen: new(5)}, ev(&controllers.SeriesInfoDetail{MembersSeen: new(5)}).SeriesInfo)
	assert.Equal(t, &dto.SourceAddDetail{Blurbs: new(6)}, ev(&controllers.SourceAddDetail{Blurbs: new(6)}).SourceAdd)

	w := ev(&controllers.WorkDetail{})
	assert.Nil(t, w.Poll, "only the kind's member is set")
	assert.Equal(t, dto.JobEvent{Event: "wait"}, jobEvent(controllers.JobEvent{Event: model.JobEventWait}),
		"no detail (unknown kind): outcome only")
	assert.Equal(t, dto.JobEvent{Event: "done"}, ev((*controllers.WorkDetail)(nil)), "a nil detail adds nothing")
}

func TestJobOutcome(t *testing.T) {
	nb := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	assert.Equal(t, dto.JobOutcome{
		Attempt: new(2), Target: new("7"), Duration: new(dto.Duration(1500 * time.Millisecond)),
		ErrorCode: new(dto.JobErrorCode("not_found")), ErrorDetail: new("x:y"), NextState: new(dto.JobState("failed")),
		NotBeforeAt: new(nb.UTC()),
	}, jobOutcome(controllers.JobOutcome{Attempt: new(2), Target: new("7"), MS: new(int64(1500)), Error: new("not_found:x:y"),
		NextState: new("failed"), NotBefore: new(nb)}))

	got := jobOutcome(controllers.JobOutcome{Error: new("rate_limited"), MS: new(int64(0))})
	assert.Equal(t, new(dto.JobErrorCode("rate_limited")), got.ErrorCode)
	assert.Nil(t, got.ErrorDetail, "no detail part: null, not a hashed empty string")
	assert.Equal(t, new(dto.Duration(0)), got.Duration, "zero is kept")
	assert.Equal(t, dto.JobOutcome{}, jobOutcome(controllers.JobOutcome{}), "unset stays nil")
}

func TestPollDetail(t *testing.T) {
	assert.Equal(t, dto.PollDetail{Page: new(1), Blurbs: new(20), New: new(0), Changed: new(2), WorkJobs: new(3),
		RequeuedUnfetched: new(4), Continue: new(false), Total: new(-1), ReqID: new("r")},
		pollDetail(controllers.PollDetail{Page: new(1), Blurbs: new(20), New: new(0), Changed: new(2), WorkJobs: new(3),
			RequeuedUnfetched: new(4), Continue: new(false), Total: new(-1), ReqID: new("r")}))
	assert.Equal(t, dto.PollDetail{}, pollDetail(controllers.PollDetail{}))
}

func TestBackfillDetail(t *testing.T) {
	assert.Equal(t, dto.BackfillDetail{Page: new(1), Blurbs: new(2), New: new(3), Rescan: new(true), WorkJobs: new(4),
		Total: new(5), ReqID: new("r"), NextPage: new(2), WindowStartOn: new("2020-01-01"), WindowEndOn: new("2020-12-31"),
		ShrinkSpanDays: new(6), TruncatedAtMaxResults: new(true), TotalBeyondPages: new(true), StaleAfterFetch: new(true),
		Stale: new(true), CursorPage: new(7), BlockedParser: new(8), RetryAt: new(time.Unix(1700000000, 0).UTC())},
		backfillDetail(controllers.BackfillDetail{Page: new(1), Blurbs: new(2), New: new(3), Rescan: new(true), WorkJobs: new(4),
			Total: new(5), ReqID: new("r"), NextPage: new(2), WindowFrom: new("2020-01-01"), WindowTo: new("2020-12-31"),
			ShrinkSpanDays: new(6), TruncatedAtMaxResults: new(true), TotalBeyondPages: new(true), StaleAfterFetch: new(true),
			Stale: new(true), CursorPage: new(7), BlockedParser: new(8), RetryAt: new(int64(1700000000))}))
	assert.Equal(t, dto.BackfillDetail{}, backfillDetail(controllers.BackfillDetail{}))
}

func TestWorkDetail(t *testing.T) {
	assert.Equal(t, dto.WorkDetail{WorkID: new(int64(7)), Class: new(dto.RequestClass("ok")), Bytes: new(100), ReqID: new("r"),
		Status: new(dto.WorkStatus("deleted")), Chapters: new(2), Words: new(300), Content: new(dto.ContentChange("new")),
		Revived: new(false), LeftSources: new(int64(1)), ImagesQueued: new(0), MetaChanged: []string{"title"},
		ChapterCountMismatch: new(true)},
		workDetail(controllers.WorkDetail{WorkID: new(int64(7)), Class: new("ok"), Bytes: new(100), ReqID: new("r"),
			Status: new("deleted"), Chapters: new(2), Words: new(300), Content: new("new"), Revived: new(false),
			LeftSources: new(int64(1)), ImagesQueued: new(0), MetaChanged: []string{"title"}, ChapterCountMismatch: new(true)}))
	assert.Equal(t, dto.WorkDetail{}, workDetail(controllers.WorkDetail{}))
}

func TestTagInfoDetail(t *testing.T) {
	assert.Equal(t, dto.TagInfoDetail{TagID: new(int64(3)), Class: new(dto.RequestClass("ok")), ReqID: new("r"),
		Type: new(dto.TagType("Fandom")), Canonical: new(false), Parents: new(2)},
		tagInfoDetail(controllers.TagInfoDetail{TagID: new(int64(3)), Class: new("ok"), ReqID: new("r"), Type: new("Fandom"),
			Canonical: new(false), Parents: new(2)}))
	assert.Equal(t, dto.TagInfoDetail{}, tagInfoDetail(controllers.TagInfoDetail{}))
}

func TestSeriesInfoDetail(t *testing.T) {
	assert.Equal(t, dto.SeriesInfoDetail{SeriesID: new(int64(4)), Class: new(dto.RequestClass("ok")), ReqID: new("r"),
		WorksCount: new(5), KnownWorksRefreshed: new(1), MembersSeen: new(5)},
		seriesInfoDetail(controllers.SeriesInfoDetail{SeriesID: new(int64(4)), Class: new("ok"), ReqID: new("r"),
			WorksCount: new(5), KnownWorksRefreshed: new(1), MembersSeen: new(5)}))
	assert.Equal(t, dto.SeriesInfoDetail{}, seriesInfoDetail(controllers.SeriesInfoDetail{}))
}

func TestSourceAddDetail(t *testing.T) {
	assert.Equal(t, dto.SourceAddDetail{SourceID: new(int64(1)), State: new(dto.SourceState("active")), Blurbs: new(20),
		Estimate: &dto.Estimate{Total: 40, Duration: dto.Duration(time.Minute)}},
		sourceAddDetail(controllers.SourceAddDetail{SourceID: new(int64(1)), State: new("active"), Blurbs: new(20),
			Estimate: &controllers.Estimate{Total: 40, Duration: time.Minute}}))
	assert.Equal(t, dto.SourceAddDetail{}, sourceAddDetail(controllers.SourceAddDetail{}))
}

func TestWorkDiag(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	d := store.WorkDiag{
		Status: model.StatusDeleted, Published: "2025-01-02", Revised: "2025-03-04", FetchedRevised: "2025-02-03",
		Words: 1200, ChaptersPosted: 2, ChaptersExpected: 3,
		Stats:      model.Stats{Kudos: 1, Hits: 2, Comments: 3, Bookmarks: 4},
		Restricted: true, Anonymous: true, ParserVersion: 5,
		FirstSeen: at, LastListed: at.Add(time.Hour), LastFetched: at.Add(2 * time.Hour),
		LastChecked: at.Add(3 * time.Hour), FetchFailedAt: at.Add(4 * time.Hour),
		Tags: 6, Creators: 7, ChapterRows: 8, Sources: 9, Images: 10, RawVersions: 11, RawBytes: 12, ChapterBodies: 13,
	}
	assert.Equal(t, dto.WorkDiag{
		ID: 9, Status: "deleted", PublishedOn: "2025-01-02", RevisedOn: "2025-03-04", FetchedRevisedOn: "2025-02-03",
		Words: 1200, ChaptersPosted: 2, ChaptersExpected: 3,
		Stats:      dto.WorkStats{Kudos: 1, Hits: 2, Comments: 3, Bookmarks: 4},
		Restricted: true, Anonymous: true, ParserVersion: 5,
		FirstSeenAt: at.UTC(), LastListedAt: at.Add(time.Hour).UTC(), LastFetchedAt: at.Add(2 * time.Hour).UTC(),
		LastCheckedAt: at.Add(3 * time.Hour).UTC(), FetchFailedAt: at.Add(4 * time.Hour).UTC(),
		Rows: dto.WorkRows{Tags: 6, Creators: 7, Chapters: 8, Sources: 9, Images: 10, RawPages: 11, RawBytes: 12, ChapterBodies: 13},
	}, workDiag(9, d))

	z := workDiag(1, store.WorkDiag{Status: model.StatusOK})
	assert.True(t, z.FirstSeenAt.IsZero() && z.FetchFailedAt.IsZero(), "unset times stay zero (omitted)")
	assert.Empty(t, z.PublishedOn)
}
