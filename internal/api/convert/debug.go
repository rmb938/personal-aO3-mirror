package convert

import (
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// JobDebug maps one job's trace.
func JobDebug(d controllers.JobDebug) dto.JobDebug {
	return dto.JobDebug{
		Job:       Job(d.Job),
		DedupeKey: d.Job.DedupeKey,
		Events:    mapSlice(d.Events, jobEvent),
		Requests:  mapSlice(d.Requests, requestRow),
	}
}

// WorkDebug maps one work's diagnostic view.
func WorkDebug(d controllers.WorkDebug) dto.WorkDebug {
	return dto.WorkDebug{
		Work:     workDiag(d.ID, d.Diag),
		Jobs:     mapSlice(d.Jobs, Job),
		Requests: mapSlice(d.Requests, requestRow),
	}
}

// jobEvent maps one event, its detail into the member of its job kind.
func jobEvent(e controllers.JobEvent) dto.JobEvent {
	out := dto.JobEvent{OccurredAt: utc(e.Time), Event: dto.JobEventType(e.Event), Outcome: jobOutcome(e.Outcome)}
	switch d := e.Detail.(type) {
	case *controllers.PollDetail:
		out.Poll = convPtr(d, pollDetail)
	case *controllers.BackfillDetail:
		out.Backfill = convPtr(d, backfillDetail)
	case *controllers.WorkDetail:
		out.Work = convPtr(d, workDetail)
	case *controllers.TagInfoDetail:
		out.TagInfo = convPtr(d, tagInfoDetail)
	case *controllers.SeriesInfoDetail:
		out.SeriesInfo = convPtr(d, seriesInfoDetail)
	case *controllers.SourceAddDetail:
		out.SourceAdd = convPtr(d, sourceAddDetail)
	}
	return out
}

func jobOutcome(o controllers.JobOutcome) dto.JobOutcome {
	out := dto.JobOutcome{
		Attempt:     o.Attempt,
		Target:      o.Target,
		Duration:    convPtr(o.MS, func(ms int64) dto.Duration { return dto.Duration(time.Duration(ms) * time.Millisecond) }),
		NextState:   enumPtr[dto.JobState](o.NextState),
		NotBeforeAt: convPtr(o.NotBefore, utc),
	}
	if o.Error != nil {
		code, detail := splitError(*o.Error)
		out.ErrorCode = &code
		if detail != "" {
			out.ErrorDetail = &detail
		}
	}
	return out
}

func pollDetail(d controllers.PollDetail) dto.PollDetail {
	return dto.PollDetail{
		Page: d.Page, Blurbs: d.Blurbs, New: d.New, Changed: d.Changed, WorkJobs: d.WorkJobs,
		RequeuedUnfetched: d.RequeuedUnfetched, Continue: d.Continue, Total: d.Total, ReqID: d.ReqID,
	}
}

func backfillDetail(d controllers.BackfillDetail) dto.BackfillDetail {
	return dto.BackfillDetail{
		Page: d.Page, Blurbs: d.Blurbs, New: d.New, Rescan: d.Rescan, WorkJobs: d.WorkJobs, Total: d.Total,
		ReqID: d.ReqID, NextPage: d.NextPage, WindowStartOn: d.WindowFrom, WindowEndOn: d.WindowTo,
		ShrinkSpanDays: d.ShrinkSpanDays, TruncatedAtMaxResults: d.TruncatedAtMaxResults,
		TotalBeyondPages: d.TotalBeyondPages, StaleAfterFetch: d.StaleAfterFetch, Stale: d.Stale,
		CursorPage: d.CursorPage, BlockedParser: d.BlockedParser,
		RetryAt: convPtr(d.RetryAt, func(sec int64) time.Time { return time.Unix(sec, 0).UTC() }),
	}
}

func workDetail(d controllers.WorkDetail) dto.WorkDetail {
	return dto.WorkDetail{
		WorkID: d.WorkID, Class: enumPtr[dto.RequestClass](d.Class), Bytes: d.Bytes, ReqID: d.ReqID,
		Status: enumPtr[dto.WorkStatus](d.Status), Chapters: d.Chapters, Words: d.Words,
		Content: enumPtr[dto.ContentChange](d.Content), Revived: d.Revived, LeftSources: d.LeftSources,
		ImagesQueued: d.ImagesQueued, MetaChanged: d.MetaChanged, ChapterCountMismatch: d.ChapterCountMismatch,
	}
}

func tagInfoDetail(d controllers.TagInfoDetail) dto.TagInfoDetail {
	return dto.TagInfoDetail{
		TagID: d.TagID, Class: enumPtr[dto.RequestClass](d.Class), ReqID: d.ReqID, Type: enumPtr[dto.TagType](d.Type),
		Canonical: d.Canonical, Parents: d.Parents,
	}
}

func seriesInfoDetail(d controllers.SeriesInfoDetail) dto.SeriesInfoDetail {
	return dto.SeriesInfoDetail{
		SeriesID: d.SeriesID, Class: enumPtr[dto.RequestClass](d.Class), ReqID: d.ReqID, WorksCount: d.WorksCount,
		KnownWorksRefreshed: d.KnownWorksRefreshed, MembersSeen: d.MembersSeen,
	}
}

func sourceAddDetail(d controllers.SourceAddDetail) dto.SourceAddDetail {
	return dto.SourceAddDetail{
		SourceID: d.SourceID, State: enumPtr[dto.SourceState](d.State), Blurbs: d.Blurbs,
		Estimate: convPtr(d.Estimate, estimate),
	}
}

func workDiag(id int64, d store.WorkDiag) dto.WorkDiag {
	return dto.WorkDiag{
		ID:               id,
		Status:           dto.WorkStatus(d.Status),
		PublishedOn:      d.Published,
		RevisedOn:        d.Revised,
		FetchedRevisedOn: d.FetchedRevised,
		Words:            d.Words,
		ChaptersPosted:   d.ChaptersPosted,
		ChaptersExpected: d.ChaptersExpected,
		Stats: dto.WorkStats{
			Kudos: d.Stats.Kudos, Hits: d.Stats.Hits, Comments: d.Stats.Comments, Bookmarks: d.Stats.Bookmarks,
		},
		Restricted:    d.Restricted,
		Anonymous:     d.Anonymous,
		ParserVersion: d.ParserVersion,
		FirstSeenAt:   utc(d.FirstSeen),
		LastListedAt:  utc(d.LastListed),
		LastFetchedAt: utc(d.LastFetched),
		LastCheckedAt: utc(d.LastChecked),
		FetchFailedAt: utc(d.FetchFailedAt),
		Rows: dto.WorkRows{
			Tags: d.Tags, Creators: d.Creators, Chapters: d.ChapterRows, Sources: d.Sources, Images: d.Images,
			RawPages: d.RawVersions, RawBytes: d.RawBytes, ChapterBodies: d.ChapterBodies,
		},
	}
}
