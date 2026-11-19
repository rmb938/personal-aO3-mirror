package model

import "time"

// SourceState values match the sources.state CHECK constraint.
type SourceState string

const (
	SourcePendingConfirm SourceState = "pending_confirm"
	SourceActive         SourceState = "active"
	SourcePaused         SourceState = "paused"
	SourceCancelled      SourceState = "cancelled"
	SourceDoneBackfill   SourceState = "done_backfill"
)

// SourceStates lists every SourceState.
func SourceStates() []SourceState {
	return []SourceState{SourcePendingConfirm, SourceActive, SourcePaused, SourceCancelled, SourceDoneBackfill}
}

// Source is one sources row. Target and FiltersJSON may hold tag/user names: never log them.
type Source struct {
	ID             int64
	Kind           string
	Target         string
	FiltersJSON    string
	Path           string
	State          SourceState
	BackfillOrder  string
	PollInterval   time.Duration
	Priority       int
	TotalWorks     int // -1 unknown
	EstRequests    int // -1 unknown
	BackfillCursor string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastPollAt     time.Time
	NextPollAt     time.Time
	LastFullScanAt time.Time
	BackfillDoneAt time.Time
	LastError      string
	MemberWorks    int // works linked via source_works (filled by the store's Get/List)
	FetchedWorks   int // of those, fetched at least once
}
