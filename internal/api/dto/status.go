package dto

import "time"

// DaemonPhase is what the scheduler loop is doing (an enum).
type DaemonPhase string

// EnumValues lists every DaemonPhase.
func (DaemonPhase) EnumValues() []string {
	return []string{"idle", "running", "between", "stopped"}
}

// WorkStatus is a work's local availability state (an enum; key `status`, shared with
// HealthStatus and ImageStatus).
type WorkStatus string

// EnumValues lists every WorkStatus.
func (WorkStatus) EnumValues() []string {
	return []string{"ok", "deleted", "hidden", "restricted", "unrevealed"}
}

// ImageStatus is an image's fetch state (an enum; key `status`).
type ImageStatus string

// EnumValues lists every ImageStatus.
func (ImageStatus) EnumValues() []string {
	return []string{"queued", "ok", "failed", "skipped", "too_large", "disallowed"}
}

// RequestClass is the outcome category of one AO3 response (an enum).
type RequestClass string

// EnumValues lists every RequestClass.
func (RequestClass) EnumValues() []string {
	return []string{"ok", "not_found", "restricted", "hidden", "adult_gate", "rate_limited", "unavailable",
		"parse_unexpected"}
}

// RequestKind is what a request expected to fetch (an enum; key `kind`, shared with SourceKind
// and JobKind).
type RequestKind string

// EnumValues lists every RequestKind.
func (RequestKind) EnumValues() []string {
	return []string{"robots", "listing", "work", "tag", "series", "page"}
}

// TagInfoType is a tag type whose wrangling info is fetched (an enum).
type TagInfoType string

// EnumValues lists every TagInfoType.
func (TagInfoType) EnumValues() []string {
	return []string{"Fandom", "Relationship", "Character", "Freeform"}
}

// Status is the daemon, queue and progress overview (GET /api/v1/status). Count lists hold only
// the values that have rows, sorted by value.
type Status struct {
	Daemon        Daemon         `json:"daemon"`
	Works         WorkCounts     `json:"works"`
	Tags          int            `json:"tags"`
	Creators      int            `json:"creators"`
	Series        int            `json:"series"`
	RawPages      RawPageCounts  `json:"raw_pages"`
	Requests      RequestCounts  `json:"requests"`
	Sources       []SourceCount  `json:"sources"`
	Images        ImageCounts    `json:"images"`
	TagInfo       TagInfo        `json:"tag_info"`
	Jobs          []JobCount     `json:"jobs"`
	PartialSeries []SeriesGap    `json:"partial_series"`
	RequestErrors []RequestError `json:"request_errors"`
	FailedJobs    []Job          `json:"failed_jobs"`
}

// Daemon is the scheduler's last heartbeat. Zero fields are omitted.
type Daemon struct {
	// Running means a heartbeat within the last 2 minutes that is not a clean stop.
	Running bool `json:"running"`
	// HeartbeatAt is when the daemon last wrote its state (omitted when it never ran).
	HeartbeatAt time.Time   `json:"heartbeat_at,omitzero"`
	PID         int         `json:"pid,omitzero"`
	Phase       DaemonPhase `json:"phase,omitzero"`
	// CurrentJob is the running job's id.
	CurrentJob int64 `json:"current_job,omitzero"`
	// WaitingUntilAt is when a waiting request gets its slot.
	WaitingUntilAt time.Time `json:"waiting_until_at,omitzero"`
	Pacer          Pacer     `json:"pacer"`
}

// Pacer is the AO3 request pacer's state.
type Pacer struct {
	IntervalMS  int64   `json:"interval_ms"`
	SlowFactor  float64 `json:"slow_factor"`
	ConsecFails int     `json:"consec_fails"`
	BreakerOpen bool    `json:"breaker_open"`
	DayCount    int     `json:"day_count"`
	// DailyBudget is the daily request budget (0: none).
	DailyBudget    int       `json:"daily_budget"`
	BackoffUntilAt time.Time `json:"backoff_until_at,omitzero"`
}

// WorkCounts counts works.
type WorkCounts struct {
	ByStatus []WorkStatusCount `json:"by_status"`
	Fetched  int               `json:"fetched"`
	// NeverFetched counts ok works with no fetched page yet.
	NeverFetched int `json:"never_fetched"`
	// ParseFailures counts fetched works the current parser could not parse.
	ParseFailures int `json:"parse_failures"`
}

// WorkStatusCount is the number of works in one status.
type WorkStatusCount struct {
	Status WorkStatus `json:"status"`
	Count  int        `json:"count"`
}

// RawPageCounts sizes the stored raw pages.
type RawPageCounts struct {
	Count int `json:"count"`
	// Bytes is their uncompressed size, ZstdBytes the stored size.
	Bytes     int64 `json:"bytes"`
	ZstdBytes int64 `json:"zstd_bytes"`
}

// RequestCounts counts AO3 requests.
type RequestCounts struct {
	Last1h  int `json:"last_1h"`
	Last24h int `json:"last_24h"`
	// ByClass24h splits the last 24 hours by outcome.
	ByClass24h []RequestClassCount `json:"by_class_24h"`
}

// RequestClassCount is the number of requests with one outcome.
type RequestClassCount struct {
	Class RequestClass `json:"class"`
	Count int          `json:"count"`
}

// SourceCount is the number of sources in one state.
type SourceCount struct {
	State SourceState `json:"state"`
	Count int         `json:"count"`
}

// ImageCounts counts images.
type ImageCounts struct {
	ByStatus []ImageStatusCount `json:"by_status"`
	// Bytes is the size of the stored images.
	Bytes int64 `json:"bytes"`
}

// ImageStatusCount is the number of images in one status.
type ImageStatusCount struct {
	Status ImageStatus `json:"status"`
	Count  int         `json:"count"`
}

// TagInfo is the tag info fetch progress over tags of the configured types used by a mirrored
// work.
type TagInfo struct {
	// Types are the configured tag types ([] when tag info is off).
	Types   []TagInfoType `json:"types"`
	Total   int           `json:"total"`
	Fetched int           `json:"fetched"`
	// Due counts tags never fetched or older than the refresh interval.
	Due int `json:"due"`
}

// JobCount is the number of jobs of one kind in one state.
type JobCount struct {
	Kind  JobKind  `json:"kind"`
	State JobState `json:"state"`
	Count int      `json:"count"`
}

// SeriesGap is a series that is not a source and has fewer mirrored works than AO3 lists.
type SeriesGap struct {
	ID int64 `json:"id"`
	// WorksTotal is AO3's count, Mirrored the works we have fetched.
	WorksTotal int `json:"works_total"`
	Mirrored   int `json:"mirrored"`
	// MissingPositions are the series part numbers none of our works has.
	MissingPositions []int `json:"missing_positions"`
	// MissingWorkIDs are known member works we have not fetched.
	MissingWorkIDs []int64 `json:"missing_work_ids"`
}

// RequestError is one recent AO3 request whose outcome was not ok.
type RequestError struct {
	RequestedAt time.Time `json:"requested_at"`
	// ReqID is the request id in the logs (hashed).
	ReqID string `json:"req_id"`
	// JobID is the job that made it (omitted when none).
	JobID int64       `json:"job_id,omitzero"`
	Kind  RequestKind `json:"kind"`
	// Path is the request path with names hashed by the scraper (hashed again).
	Path string `json:"path"`
	// Status is the HTTP status (0 when the request failed in transport).
	Status int          `json:"status"`
	Class  RequestClass `json:"class"`
	// Reason is the classifier's reason (hashed).
	Reason     string `json:"reason"`
	Bytes      int    `json:"bytes"`
	DurationMS int64  `json:"duration_ms"`
	WaitedMS   int64  `json:"waited_ms"`
	// Hop is the redirect hop (0: the first request).
	Hop int `json:"hop"`
}
