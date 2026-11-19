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

// Job event types (job_events.event), written by the scheduler.
const (
	JobEventStart  = "start"
	JobEventDone   = "done"
	JobEventFailed = "failed"
	JobEventRetry  = "retry" // a retryable error: requeued, or failed once attempts run out
	JobEventError  = "error" // an unexpected error, retried like retry
	JobEventWait   = "wait"  // nothing was sent: requeued without using an attempt
)

// JobEvents lists every job event type.
func JobEvents() []string {
	return []string{JobEventStart, JobEventDone, JobEventFailed, JobEventRetry, JobEventError, JobEventWait}
}

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
