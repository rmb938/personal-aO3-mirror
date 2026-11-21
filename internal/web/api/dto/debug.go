package dto

import "time"

// JobEventType is what happened to a job in one trace event (an enum).
type JobEventType string

// EnumValues lists every JobEventType.
func (JobEventType) EnumValues() []string {
	return []string{"start", "done", "failed", "retry", "error", "wait"}
}

// JobDebug is one job's content-free trace (GET /api/v1/debug/jobs/{id}, admin socket).
type JobDebug struct {
	Job Job `json:"job"`
	// DedupeKey is the key that keeps a second copy of the job from being queued (hashed).
	DedupeKey string `json:"dedupe_key"`
	// Events are the job's trace events, oldest first.
	Events []JobEvent `json:"events"`
	// Requests are the job's last 20 AO3 requests, newest first.
	Requests []Request `json:"requests"`
}

// JobEvent is one job trace event: the scheduler's outcome and the detail of the job's kind,
// in the member named after the kind (the others are omitted). Every outcome and detail
// field is present, null when the run did not set it. A start event sets only the outcome's
// attempt and target.
type JobEvent struct {
	OccurredAt time.Time         `json:"occurred_at"`
	Event      JobEventType      `json:"event"`
	Outcome    JobOutcome        `json:"outcome"`
	Poll       *PollDetail       `json:"poll,omitzero"`
	Backfill   *BackfillDetail   `json:"backfill,omitzero"`
	Work       *WorkDetail       `json:"work,omitzero"`
	TagInfo    *TagInfoDetail    `json:"tag_info,omitzero"`
	SeriesInfo *SeriesInfoDetail `json:"series_info,omitzero"`
	SourceAdd  *SourceAddDetail  `json:"source_add,omitzero"`
}

// JobOutcome is what the scheduler records on a job event.
type JobOutcome struct {
	// Attempt (start event) is the attempt number, counting this one.
	Attempt *int `json:"attempt"`
	// Target (start event) is the job's target.
	Target *int64 `json:"target"`
	// Duration is how long the handler ran.
	Duration *Duration `json:"duration"`
	// ErrorCode and ErrorDetail are the run's error, split at the first ':' (the detail is
	// hashed, and null when the error has none).
	ErrorCode   *JobErrorCode `json:"error_code"`
	ErrorDetail *string       `json:"error_detail"`
	// NextState is the job's state after a retryable error: queued again, or failed when out
	// of attempts.
	NextState *JobState `json:"next_state"`
	// NotBeforeAt holds the requeued job back until then.
	NotBeforeAt *time.Time `json:"not_before_at"`
}

// PollDetail is one update poll page run.
type PollDetail struct {
	Page   *int `json:"page"`
	Blurbs *int `json:"blurbs"`
	// New and Changed count works new to the mirror and works whose listing changed.
	New     *int `json:"new"`
	Changed *int `json:"changed"`
	// WorkJobs counts the work fetches queued; RequeuedUnfetched the member works queued again
	// because an earlier fetch failed or never ran.
	WorkJobs          *int `json:"work_jobs"`
	RequeuedUnfetched *int `json:"requeued_unfetched"`
	// Continue: the poll goes on to the next page.
	Continue *bool `json:"continue"`
	// Total is the listing's work count (-1 when it shows none).
	Total *int `json:"total"`
	// ReqID is the page request's id in the logs (hashed).
	ReqID *string `json:"req_id"`
}

// BackfillDetail is one backfill page run.
type BackfillDetail struct {
	Page     *int  `json:"page"`
	Blurbs   *int  `json:"blurbs"`
	New      *int  `json:"new"`
	Rescan   *bool `json:"rescan"`
	WorkJobs *int  `json:"work_jobs"`
	// Total is the listing's work count (-1 when it shows none).
	Total *int `json:"total"`
	// ReqID is the page request's id in the logs (hashed).
	ReqID    *string `json:"req_id"`
	NextPage *int    `json:"next_page"`
	// WindowStartOn and WindowEndOn bound the revised-date window walked (windowed sources).
	WindowStartOn *string `json:"window_start_on"`
	WindowEndOn   *string `json:"window_end_on"`
	// ShrinkSpanDays is the new window span after a window over AO3's result limit was halved.
	ShrinkSpanDays        *int  `json:"shrink_span_days"`
	TruncatedAtMaxResults *bool `json:"truncated_at_max_results"`
	// TotalBeyondPages: the listing total is more than the pages walked hold.
	TotalBeyondPages *bool `json:"total_beyond_pages"`
	// StaleAfterFetch: a refresh moved the cursor during the fetch; the cursor was left alone.
	StaleAfterFetch *bool `json:"stale_after_fetch"`
	// Stale: the cursor was already past this page (at CursorPage), so nothing was fetched.
	Stale      *bool `json:"stale"`
	CursorPage *int  `json:"cursor_page"`
	// BlockedParser is the parser version the backfill is blocked on after a parse failure.
	BlockedParser *int `json:"blocked_parser"`
	// RetryAt holds the page back after a permanent failure.
	RetryAt *time.Time `json:"retry_at"`
}

// ContentChange is how a fetched work's content compares with the stored copy (an enum).
type ContentChange string

// EnumValues lists every ContentChange.
func (ContentChange) EnumValues() []string { return []string{"new", "changed", "unchanged"} }

// WorkDetail is one work fetch run.
type WorkDetail struct {
	WorkID *int64        `json:"work_id"`
	Class  *RequestClass `json:"class"`
	Bytes  *int          `json:"bytes"`
	// ReqID is the request's id in the logs (hashed).
	ReqID *string `json:"req_id"`
	// Status is the work's new status when AO3 no longer serves it.
	Status   *WorkStatus    `json:"status"`
	Chapters *int           `json:"chapters"`
	Words    *int           `json:"words"`
	Content  *ContentChange `json:"content"`
	// Revived: the work was deleted, hidden or restricted before this fetch.
	Revived *bool `json:"revived"`
	// LeftSources counts the sources that dropped the work after missing it in rescans.
	LeftSources  *int64 `json:"left_sources"`
	ImagesQueued *int   `json:"images_queued"`
	// MetaChanged are the changed fields and related-row changes, names and counts only
	// (hashed).
	MetaChanged          []string `json:"meta_changed"`
	ChapterCountMismatch *bool    `json:"chapter_count_mismatch"`
}

// TagInfoDetail is one tag info fetch run.
type TagInfoDetail struct {
	TagID *int64        `json:"tag_id"`
	Class *RequestClass `json:"class"`
	// ReqID is the request's id in the logs (hashed).
	ReqID     *string  `json:"req_id"`
	Type      *TagType `json:"type"`
	Canonical *bool    `json:"canonical"`
	Parents   *int     `json:"parents"`
}

// SeriesInfoDetail is one series info fetch run.
type SeriesInfoDetail struct {
	SeriesID *int64        `json:"series_id"`
	Class    *RequestClass `json:"class"`
	// ReqID is the request's id in the logs (hashed).
	ReqID *string `json:"req_id"`
	// WorksCount is AO3's count of the series' works.
	WorksCount *int `json:"works_count"`
	// KnownWorksRefreshed counts the mirrored works updated from the series page.
	KnownWorksRefreshed *int `json:"known_works_refreshed"`
	MembersSeen         *int `json:"members_seen"`
}

// SourceAddDetail is one queued source add run.
type SourceAddDetail struct {
	SourceID *int64       `json:"source_id"`
	State    *SourceState `json:"state"`
	Blurbs   *int         `json:"blurbs"`
	Estimate *Estimate    `json:"estimate"`
}

// WorkDebug is one work's content-free state (GET /api/v1/debug/works/{id}, admin socket).
type WorkDebug struct {
	Work WorkDiag `json:"work"`
	// Jobs are the work's last 20 fetch jobs, newest first.
	Jobs []Job `json:"jobs"`
	// Requests are the last 20 AO3 requests for the work's pages, newest first.
	Requests []Request `json:"requests"`
}

// WorkDiag is a work's numbers, dates and states, with no content. Zero dates and times are
// omitted.
type WorkDiag struct {
	ID          int64      `json:"id"`
	Status      WorkStatus `json:"status"`
	PublishedOn string     `json:"published_on,omitzero"`
	RevisedOn   string     `json:"revised_on,omitzero"`
	// FetchedRevisedOn is the revised date at the last full fetch.
	FetchedRevisedOn string `json:"fetched_revised_on,omitzero"`
	Words            int    `json:"words"`
	ChaptersPosted   int    `json:"chapters_posted"`
	// ChaptersExpected is omitted when unknown.
	ChaptersExpected int       `json:"chapters_expected,omitzero"`
	Stats            WorkStats `json:"stats"`
	Restricted       bool      `json:"restricted"`
	Anonymous        bool      `json:"anonymous"`
	// ParserVersion is the parser that last parsed the work (0: never, or the parse failed).
	ParserVersion int       `json:"parser_version"`
	FirstSeenAt   time.Time `json:"first_seen_at,omitzero"`
	LastListedAt  time.Time `json:"last_listed_at,omitzero"`
	LastFetchedAt time.Time `json:"last_fetched_at,omitzero"`
	LastCheckedAt time.Time `json:"last_checked_at,omitzero"`
	// FetchFailedAt is the last full fetch that gave up (omitted after a success).
	FetchFailedAt time.Time `json:"fetch_failed_at,omitzero"`
	Rows          WorkRows  `json:"rows"`
}

// WorkRows counts the stored rows that belong to a work.
type WorkRows struct {
	Tags     int `json:"tags"`
	Creators int `json:"creators"`
	Chapters int `json:"chapters"`
	Sources  int `json:"sources"`
	Images   int `json:"images"`
	// RawPages counts the stored page versions; RawBytes is their uncompressed size.
	RawPages int   `json:"raw_pages"`
	RawBytes int64 `json:"raw_bytes"`
	// ChapterBodies counts the stored chapter texts.
	ChapterBodies int `json:"chapter_bodies"`
}
