package model

import "time"

// Job states (jobs.state CHECK).
const (
	JobQueued    = "queued"
	JobRunning   = "running"
	JobDone      = "done"
	JobFailed    = "failed"
	JobCancelled = "cancelled"
)

// JobStates lists every job state.
func JobStates() []string { return []string{JobQueued, JobRunning, JobDone, JobFailed, JobCancelled} }

// Job is one queued unit of work. Target is content-free (page number, work id, path pattern).
type Job struct {
	ID          int64
	Kind        string
	SourceID    int64 // 0 = none
	Target      string
	Priority    int // class: lower runs first
	State       string
	NotBefore   time.Time
	Attempts    int
	MaxAttempts int
	LastError   string
	DedupeKey   string
	CreatedAt   time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
}

// JobFilter selects jobs; zero fields match everything. Offset, Limit and Order only apply to
// store.FindJobs (CountJobs ignores them).
type JobFilter struct {
	State        string
	Kind         string
	SourceID     int64
	DedupeKey    string // exact match
	DedupePrefix string
	NotQueued    bool // only jobs that left the queue (running, done, failed, cancelled)
	Offset       int  // rows to skip (≥ 0)
	Limit        int  // max rows (≥ 1)
	Order        JobOrder
}

// JobOrder sorts FindJobs results.
type JobOrder int

const (
	JobsNewest      JobOrder = iota // id descending (default)
	JobsOldest                      // id ascending: queue order for queued jobs
	JobsLastStarted                 // most recently started first
)
