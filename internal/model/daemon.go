package model

import "time"

// DaemonPhase is what the scheduler loop is doing.
type DaemonPhase string

const (
	DaemonIdle    DaemonPhase = "idle"    // nothing runnable; sleeping
	DaemonRunning DaemonPhase = "running" // running a job (Heartbeat.CurrentJob)
	DaemonBetween DaemonPhase = "between" // between jobs (bookkeeping, pruning)
	DaemonStopped DaemonPhase = "stopped" // shut down cleanly
)

// DaemonPhases lists every DaemonPhase.
func DaemonPhases() []DaemonPhase {
	return []DaemonPhase{DaemonIdle, DaemonRunning, DaemonBetween, DaemonStopped}
}

// Heartbeat is the daemon's content-free state, written by the scheduler and read by `status`.
// Zero times mean unset.
type Heartbeat struct {
	At           time.Time // when it was last written (zero: the daemon never ran)
	PID          int
	Phase        DaemonPhase // "" before the loop's first phase change
	CurrentJob   int64       // 0 = none
	WaitingUntil time.Time   // a request is waiting for its slot until then
	// Pacer state (httpx.PacerState).
	Interval     time.Duration
	SlowFactor   float64
	ConsecFails  int
	BreakerOpen  bool
	DayCount     int
	DailyBudget  int // 0 = no budget
	BackoffUntil time.Time
}
