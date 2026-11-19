package dto

import "time"

// List is one page of a list route: {items, page, per_page, total}.
type List[T any] struct {
	Items   []T   `json:"items"`
	Page    int64 `json:"page"`
	PerPage int64 `json:"per_page"`
	// Total is the number of items over all pages.
	Total int `json:"total"`
}

// SourceKind is what a source mirrors (an enum).
type SourceKind string

// EnumValues lists every SourceKind.
func (SourceKind) EnumValues() []string {
	return []string{"tag", "user", "pseud", "user_bookmarks", "series", "collection", "work"}
}

// SourceState is a source's lifecycle state (an enum; key `state`, shared with JobState).
type SourceState string

// EnumValues lists every SourceState.
func (SourceState) EnumValues() []string {
	return []string{"pending_confirm", "active", "paused", "cancelled", "done_backfill"}
}

// JobState is a job's state (an enum; key `state`, shared with SourceState).
type JobState string

// EnumValues lists every JobState.
func (JobState) EnumValues() []string {
	return []string{"queued", "running", "done", "failed", "cancelled"}
}

// Order is a listing sort, "<field>_<asc|desc>" (an enum).
type Order string

// orderFields are the sortable listing fields.
var orderFields = []string{"author", "bookmarks", "comments", "created", "hits", "kudos", "revised", "title", "words"}

// EnumValues lists every Order.
func (Order) EnumValues() []string {
	vals := make([]string, 0, 2*len(orderFields))
	for _, f := range orderFields {
		vals = append(vals, f+"_asc", f+"_desc")
	}
	return vals
}

// JobErrorCode is the code part of a job's last error, before the first ':' (an enum).
type JobErrorCode string

// EnumValues lists every JobErrorCode.
func (JobErrorCode) EnumValues() []string {
	return []string{
		"parse_unexpected", "parse_error", "not_found", "restricted", "hidden", "adult_gate",
		"too_large", "robots_disallowed", "robots_unavailable", "unexpected_response",
		"bad_source", "bad_filters", "bad_cursor", "bad_work_id", "bad_series_id", "no_source", "no_tag",
		"panic", "internal", "source_exists", "no_source_add",
		"rate_limited", "unavailable", "crashed",
	}
}

// Source is one mirrored source (GET /api/v1/sources, /api/v1/sources/{id}).
type Source struct {
	ID   int64      `json:"id"`
	Kind SourceKind `json:"kind"`
	// Target is the tag, user, collection name or series/work ID (hashed).
	Target string `json:"target"`
	// TargetID is the numeric target of a series or work source (omitted otherwise).
	TargetID int64 `json:"target_id,omitzero"`
	// Filters are the listing's work_search filters, sorted by name ([] when none).
	Filters []Filter `json:"filters"`
	// Path is the AO3 listing path (hashed: it holds the target).
	Path  string      `json:"path"`
	State SourceState `json:"state"`
	// Order is the backfill sort (omitted when the source has none, e.g. a work).
	Order         Order `json:"order,omitzero"`
	PollIntervalS int64 `json:"poll_interval_s"`
	Priority      int   `json:"priority"`
	// TotalWorks and EstRequests are null until known.
	TotalWorks   *int `json:"total_works"`
	EstRequests  *int `json:"est_requests"`
	MemberWorks  int  `json:"member_works"`
	FetchedWorks int  `json:"fetched_works"`
	// Backfill is the backfill position (omitted when there is none).
	Backfill       *Backfill `json:"backfill,omitzero"`
	CreatedAt      time.Time `json:"created_at,omitzero"`
	UpdatedAt      time.Time `json:"updated_at,omitzero"`
	LastPollAt     time.Time `json:"last_poll_at,omitzero"`
	NextPollAt     time.Time `json:"next_poll_at,omitzero"`
	LastFullScanAt time.Time `json:"last_full_scan_at,omitzero"`
	BackfillDoneAt time.Time `json:"backfill_done_at,omitzero"`
	// LastError is the last poll or backfill error, e.g. "poll page N: code" (hashed whole).
	LastError string `json:"last_error,omitzero"`
}

// Filter is one work_search filter of a source.
type Filter struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// Backfill is a source's backfill cursor.
type Backfill struct {
	// NextPage is the next listing page; 0 means the listing walk is done.
	NextPage int   `json:"next_page"`
	Order    Order `json:"order,omitzero"`
	// WindowStartOn and WindowEndOn bound the revised-date window being walked, for a source
	// over AO3's 100k result cap.
	WindowStartOn string `json:"window_start_on,omitzero"`
	WindowEndOn   string `json:"window_end_on,omitzero"`
	// Rescan marks a periodic full re-walk, started at RescanStartedAt.
	Rescan          bool      `json:"rescan,omitzero"`
	RescanStartedAt time.Time `json:"rescan_started_at,omitzero"`
	// BlockedParser is the parser version that failed on NextPage; backfill waits for another.
	BlockedParser int `json:"blocked_parser,omitzero"`
	// RetryAt holds NextPage back after a permanent failure that is not a parse failure.
	RetryAt time.Time `json:"retry_at,omitzero"`
}

// SourceAdd is a queued source add (GET /api/v1/source-adds/{job_id}).
type SourceAdd struct {
	JobID int64    `json:"job_id"`
	State JobState `json:"state"`
	// LastErrorCode and LastErrorDetail are the job's last error, split at the first ':'.
	LastErrorCode   JobErrorCode `json:"last_error_code,omitzero"`
	LastErrorDetail string       `json:"last_error_detail,omitzero"`
	// Result is set once the add is done.
	Result *SourceAddResult `json:"result,omitzero"`
}

// SourceAddResult is what a done source add created.
type SourceAddResult struct {
	Source Source `json:"source"`
	// Blurbs is the number of listing blurbs stored from page 1.
	Blurbs   int      `json:"blurbs"`
	Estimate Estimate `json:"estimate"`
}

// Estimate is the projected cost of a source's full backfill.
type Estimate struct {
	Total           int   `json:"total"`
	Pages           int   `json:"pages"`
	ListingRequests int   `json:"listing_requests"`
	WorkFetches     int   `json:"work_fetches"`
	Requests        int   `json:"requests"`
	DurationS       int64 `json:"duration_s"`
	DiskBytes       int64 `json:"disk_bytes"`
	// Windowed means the backfill is sliced into revised-date windows (over 100k results).
	Windowed bool `json:"windowed"`
}
