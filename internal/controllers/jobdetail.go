package controllers

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/jobs"
)

// JobDetail is the content-free summary of one job run (ids, counts, classes), recorded by the
// scheduler as the job event detail. Each job kind has its own type, and its handler returns
// it. Every field is a pointer: nil when the run did not get to the step that sets it, so a
// stored detail (nil fields omitted) tells "not set" from a zero. Its json keys are the stored
// format: renaming one loses it from older events.
type JobDetail interface {
	// JobKind is the job kind (jobs.Kind*) this detail belongs to.
	JobKind() string
}

// JobOutcome is the part of every job event that the scheduler fills: the start event's attempt
// and target, and an outcome event's duration, error, next state and hold-back time. It is
// stored merged with the handler's JobDetail into one JSON object, so their keys must differ.
type JobOutcome struct {
	Attempt *int    `json:"attempt,omitzero"`
	Target  *string `json:"target,omitzero"`
	MS      *int64  `json:"ms,omitzero"`
	// Error is the error code, or "code:detail" for a permanent failure.
	Error     *string    `json:"error,omitzero"`
	NextState *string    `json:"next_state,omitzero"`
	NotBefore *time.Time `json:"not_before,omitzero"`
}

// PollDetail is one update poll page (jobs.KindPoll).
type PollDetail struct {
	Page              *int    `json:"page,omitzero"`
	Blurbs            *int    `json:"blurbs,omitzero"`
	New               *int    `json:"new,omitzero"`
	Changed           *int    `json:"changed,omitzero"`
	WorkJobs          *int    `json:"work_jobs,omitzero"`
	RequeuedUnfetched *int    `json:"requeued_unfetched,omitzero"`
	Continue          *bool   `json:"continue,omitzero"`
	Total             *int    `json:"total,omitzero"`
	ReqID             *string `json:"req_id,omitzero"`
}

// BackfillDetail is one backfill page (jobs.KindBackfill).
type BackfillDetail struct {
	Page     *int    `json:"page,omitzero"`
	Blurbs   *int    `json:"blurbs,omitzero"`
	New      *int    `json:"new,omitzero"`
	Rescan   *bool   `json:"rescan,omitzero"`
	WorkJobs *int    `json:"work_jobs,omitzero"`
	Total    *int    `json:"total,omitzero"`
	ReqID    *string `json:"req_id,omitzero"`
	NextPage *int    `json:"next_page,omitzero"`
	// WindowFrom and WindowTo (YYYY-MM-DD) bound the revised-date window walked.
	WindowFrom            *string `json:"window_from,omitzero"`
	WindowTo              *string `json:"window_to,omitzero"`
	ShrinkSpanDays        *int    `json:"shrink_span_days,omitzero"`
	TruncatedAtMaxResults *bool   `json:"truncated_at_max_results,omitzero"`
	TotalBeyondPages      *bool   `json:"total_beyond_pages,omitzero"`
	// StaleAfterFetch: a refresh moved the cursor during the fetch; the cursor was left alone.
	StaleAfterFetch *bool `json:"stale_after_fetch,omitzero"`
	// Stale: the cursor was already past this page (CursorPage), so nothing was fetched.
	Stale      *bool `json:"stale,omitzero"`
	CursorPage *int  `json:"cursor_page,omitzero"`
	// BlockedParser is the parser version the backfill is blocked on after a parse failure.
	BlockedParser *int `json:"blocked_parser,omitzero"`
	// RetryAt (unix seconds) holds the page back after a permanent failure.
	RetryAt *int64 `json:"retry_at,omitzero"`
}

// WorkDetail is one work fetch (jobs.KindWork).
type WorkDetail struct {
	WorkID *int64  `json:"work_id,omitzero"`
	Class  *string `json:"class,omitzero"`
	Bytes  *int    `json:"bytes,omitzero"`
	ReqID  *string `json:"req_id,omitzero"`
	// Status is the work's new status when AO3 no longer serves it.
	Status       *string `json:"status,omitzero"`
	Chapters     *int    `json:"chapters,omitzero"`
	Words        *int    `json:"words,omitzero"`
	Content      *string `json:"content,omitzero"` // store.ContentChange
	Revived      *bool   `json:"revived,omitzero"`
	LeftSources  *int64  `json:"left_sources,omitzero"`
	ImagesQueued *int    `json:"images_queued,omitzero"`
	// MetaChanged are the changed fields and related-row changes, names and counts only.
	MetaChanged          []string `json:"meta_changed,omitzero"`
	ChapterCountMismatch *bool    `json:"chapter_count_mismatch,omitzero"`
}

// TagInfoDetail is one tag info fetch (jobs.KindTagInfo).
type TagInfoDetail struct {
	TagID     *int64  `json:"tag_id,omitzero"`
	Class     *string `json:"class,omitzero"`
	ReqID     *string `json:"req_id,omitzero"`
	Type      *string `json:"type,omitzero"`
	Canonical *bool   `json:"canonical,omitzero"`
	Parents   *int    `json:"parents,omitzero"`
}

// SeriesInfoDetail is one series info fetch (jobs.KindSeries).
type SeriesInfoDetail struct {
	SeriesID            *int64  `json:"series_id,omitzero"`
	Class               *string `json:"class,omitzero"`
	ReqID               *string `json:"req_id,omitzero"`
	WorksCount          *int    `json:"works_count,omitzero"`
	KnownWorksRefreshed *int    `json:"known_works_refreshed,omitzero"`
	MembersSeen         *int    `json:"members_seen,omitzero"`
}

// SourceAddDetail is one queued source add (jobs.KindSourceAdd). The source add result is read
// back from it.
type SourceAddDetail struct {
	SourceID *int64    `json:"source_id,omitzero"`
	State    *string   `json:"state,omitzero"`
	Blurbs   *int      `json:"blurbs,omitzero"`
	Estimate *Estimate `json:"estimate,omitzero"`
}

func (*PollDetail) JobKind() string       { return jobs.KindPoll }
func (*BackfillDetail) JobKind() string   { return jobs.KindBackfill }
func (*WorkDetail) JobKind() string       { return jobs.KindWork }
func (*TagInfoDetail) JobKind() string    { return jobs.KindTagInfo }
func (*SeriesInfoDetail) JobKind() string { return jobs.KindSeries }
func (*SourceAddDetail) JobKind() string  { return jobs.KindSourceAdd }

// newJobDetail returns an empty detail for a job kind, or nil for an unknown kind.
func newJobDetail(kind string) JobDetail {
	switch kind {
	case jobs.KindPoll:
		return &PollDetail{}
	case jobs.KindBackfill:
		return &BackfillDetail{}
	case jobs.KindWork:
		return &WorkDetail{}
	case jobs.KindTagInfo:
		return &TagInfoDetail{}
	case jobs.KindSeries:
		return &SeriesInfoDetail{}
	case jobs.KindSourceAdd:
		return &SourceAddDetail{}
	}
	return nil
}

// deref returns *p, or the zero value for nil.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// DecodeJobEvent splits a stored job event detail into the scheduler's part and the detail of
// the job's kind (nil for an unknown kind). Each decode ignores the other's keys.
func DecodeJobEvent(kind, raw string) (JobOutcome, JobDetail, error) {
	var o JobOutcome
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		return o, nil, fmt.Errorf("job event outcome: %w", err)
	}
	d := newJobDetail(kind)
	if d == nil {
		return o, nil, nil
	}
	if err := json.Unmarshal([]byte(raw), d); err != nil {
		return o, nil, fmt.Errorf("job event %s detail: %w", kind, err)
	}
	return o, d, nil
}
