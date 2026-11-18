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
