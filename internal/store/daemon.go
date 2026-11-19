package store

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// The daemon_state keys a Heartbeat is stored under, one row each. A row's updated_at is when
// the heartbeat was written.
const (
	hbPID          = "pid"
	hbPhase        = "phase"
	hbCurrentJob   = "current_job"
	hbWaitingUntil = "waiting_until"
	hbInterval     = "interval"
	hbSlowFactor   = "slow_factor"
	hbConsecFails  = "consec_fails"
	hbBreakerOpen  = "breaker_open"
	hbDayCount     = "day_count"
	hbDailyBudget  = "daily_budget"
	hbBackoffUntil = "backoff_until"
)

// SetHeartbeat stores the daemon's heartbeat (h.At is ignored: the row time is now).
func (s *Tx) SetHeartbeat(ctx context.Context, h model.Heartbeat) error {
	kv := map[string]string{
		hbPID:          strconv.Itoa(h.PID),
		hbPhase:        string(h.Phase),
		hbCurrentJob:   strconv.FormatInt(h.CurrentJob, 10),
		hbWaitingUntil: rfc3339(h.WaitingUntil),
		hbInterval:     h.Interval.String(),
		hbSlowFactor:   strconv.FormatFloat(h.SlowFactor, 'f', 2, 64),
		hbConsecFails:  strconv.Itoa(h.ConsecFails),
		hbBreakerOpen:  strconv.FormatBool(h.BreakerOpen),
		hbDayCount:     strconv.Itoa(h.DayCount),
		hbDailyBudget:  strconv.Itoa(h.DailyBudget),
		hbBackoffUntil: rfc3339(h.BackoffUntil),
	}
	now := s.unix()
	for k, v := range kv {
		if _, err := s.meta.ExecContext(ctx, `INSERT INTO daemon_state (key, value, updated_at) VALUES (?,?,?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, k, v, now); err != nil {
			return err
		}
	}
	return nil
}

// Heartbeat returns the daemon's last heartbeat; its At is zero if the daemon never ran. A
// missing or unparsable value is left zero (the heartbeat is display-only).
func (s *ReadTx) Heartbeat(ctx context.Context) (h model.Heartbeat, err error) {
	rows, err := s.meta.QueryContext(ctx, `SELECT key, value, updated_at FROM daemon_state`)
	if err != nil {
		return h, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var latest int64
	for rows.Next() {
		var k, v string
		var at int64
		if err := rows.Scan(&k, &v, &at); err != nil {
			return h, err
		}
		latest = max(latest, at)
		switch k {
		case hbPID:
			h.PID, _ = strconv.Atoi(v)
		case hbPhase:
			h.Phase = model.DaemonPhase(v)
		case hbCurrentJob:
			h.CurrentJob, _ = strconv.ParseInt(v, 10, 64)
		case hbWaitingUntil:
			h.WaitingUntil = parseRFC3339(v)
		case hbInterval:
			h.Interval, _ = time.ParseDuration(v)
		case hbSlowFactor:
			h.SlowFactor, _ = strconv.ParseFloat(v, 64)
		case hbConsecFails:
			h.ConsecFails, _ = strconv.Atoi(v)
		case hbBreakerOpen:
			h.BreakerOpen, _ = strconv.ParseBool(v)
		case hbDayCount:
			h.DayCount, _ = strconv.Atoi(v)
		case hbDailyBudget:
			h.DailyBudget, _ = strconv.Atoi(v)
		case hbBackoffUntil:
			h.BackoffUntil = parseRFC3339(v)
		}
	}
	if err := rows.Err(); err != nil {
		return h, err
	}
	h.At = unixTime(latest)
	return h, nil
}

// rfc3339 formats t in UTC, or "" when zero.
func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// parseRFC3339 parses an rfc3339 value; "" or a bad value is the zero time.
func parseRFC3339(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
