package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

func TestHeartbeatRoundTrip(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()

	h, err := s.Heartbeat(ctx)
	require.NoError(t, err)
	require.Equal(t, model.Heartbeat{}, h, "never ran")

	at := clk.t.Add(5 * time.Minute)
	want := model.Heartbeat{
		PID: 4242, Phase: model.DaemonRunning, CurrentJob: 17,
		WaitingUntil: at, Interval: 12500 * time.Millisecond, SlowFactor: 1.5, ConsecFails: 3,
		BreakerOpen: true, DayCount: 99, DailyBudget: 500, BackoffUntil: at.Add(time.Minute),
		At: time.Unix(1, 0), // ignored on write
	}
	require.NoError(t, s.SetHeartbeat(ctx, want))
	got, err := s.Heartbeat(ctx)
	require.NoError(t, err)
	want.At = clk.t
	require.True(t, got.At.Equal(want.At), "At is the write time: %v", got.At)
	require.True(t, got.WaitingUntil.Equal(want.WaitingUntil))
	require.True(t, got.BackoffUntil.Equal(want.BackoffUntil))
	got.At, got.WaitingUntil, got.BackoffUntil = want.At, want.WaitingUntil, want.BackoffUntil
	require.Equal(t, want, got)

	// Zero times stay zero, and a later write replaces every value.
	clk.t = clk.t.Add(time.Minute)
	require.NoError(t, s.SetHeartbeat(ctx, model.Heartbeat{PID: 1, Phase: model.DaemonStopped}))
	got, err = s.Heartbeat(ctx)
	require.NoError(t, err)
	require.True(t, got.At.Equal(clk.t))
	got.At = time.Time{}
	require.Equal(t, model.Heartbeat{PID: 1, Phase: model.DaemonStopped}, got)
}

func TestHeartbeatBadValuesAreZero(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	require.NoError(t, s.SetHeartbeat(ctx, model.Heartbeat{PID: 7, DayCount: 3}))
	_, err := s.meta.ExecContext(ctx, `UPDATE daemon_state SET value = 'x' WHERE key IN (?, ?)`, hbDayCount, hbBackoffUntil)
	require.NoError(t, err)
	_, err = s.meta.ExecContext(ctx, `INSERT INTO daemon_state (key, value, updated_at) VALUES ('heartbeat', 'old', 1)`)
	require.NoError(t, err)
	got, err := s.Heartbeat(ctx)
	require.NoError(t, err, "an unknown key or bad value is not an error")
	require.Equal(t, 7, got.PID)
	require.Zero(t, got.DayCount)
	require.True(t, got.BackoffUntil.IsZero())
}
