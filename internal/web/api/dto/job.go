package dto

import "time"

// JobKind is what a job does (an enum; key `kind`, shared with SourceKind).
type JobKind string

// EnumValues lists every JobKind.
func (JobKind) EnumValues() []string {
	return []string{"poll", "backfill", "work", "tag_info", "series_info", "source_add"}
}

// JobState is a job's state (an enum; key `state`, shared with SourceState).
type JobState string

// JobQueued is the queued JobState: GET /api/v1/jobs lists those in queue order.
const JobQueued JobState = "queued"

// EnumValues lists every JobState.
func (JobState) EnumValues() []string {
	return []string{string(JobQueued), "running", "done", "failed", "cancelled"}
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

// Job is one scheduler job (GET /api/v1/jobs, /api/v1/jobs/{id}).
type Job struct {
	ID   int64   `json:"id"`
	Kind JobKind `json:"kind"`
	// Target is what the job acts on: a page number, a work/tag/series id or a source add id
	// (hashed).
	Target string `json:"target"`
	// SourceID is the job's source (omitted when it has none).
	SourceID int64 `json:"source_id,omitzero"`
	// Priority is the job's class: lower runs first.
	Priority    int      `json:"priority"`
	State       JobState `json:"state"`
	Attempts    int      `json:"attempts"`
	MaxAttempts int      `json:"max_attempts"`
	// LastErrorCode and LastErrorDetail are the job's last error, split at the first ':'.
	LastErrorCode   JobErrorCode `json:"last_error_code,omitzero"`
	LastErrorDetail string       `json:"last_error_detail,omitzero"`
	// NotBeforeAt holds a queued job back until then (omitted when unset).
	NotBeforeAt time.Time `json:"not_before_at,omitzero"`
	CreatedAt   time.Time `json:"created_at,omitzero"`
	StartedAt   time.Time `json:"started_at,omitzero"`
	FinishedAt  time.Time `json:"finished_at,omitzero"`
}
