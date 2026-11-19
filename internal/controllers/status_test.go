package controllers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

func TestDaemonRunning(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		h    model.Heartbeat
		want bool
	}{
		"never ran":     {model.Heartbeat{}, false},
		"fresh":         {model.Heartbeat{At: now.Add(-30 * time.Second), Phase: model.DaemonIdle}, true},
		"at the limit":  {model.Heartbeat{At: now.Add(-DaemonStaleAfter), Phase: model.DaemonRunning}, true},
		"stale":         {model.Heartbeat{At: now.Add(-DaemonStaleAfter - time.Second), Phase: model.DaemonIdle}, false},
		"fresh stopped": {model.Heartbeat{At: now, Phase: model.DaemonStopped}, false},
		"no phase yet":  {model.Heartbeat{At: now}, true},
	} {
		assert.Equal(t, tc.want, daemonRunning(tc.h, now), name)
	}
}

func TestStatusDaemon(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	rep, err := e.svc.Status(ctx)
	require.NoError(t, err)
	assert.True(t, rep.Daemon.At.IsZero())
	assert.False(t, rep.DaemonRunning)

	require.NoError(t, e.st.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		return tx.SetHeartbeat(ctx, model.Heartbeat{PID: 9, Phase: model.DaemonRunning, CurrentJob: 3})
	}))
	rep, err = e.svc.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, 9, rep.Daemon.PID)
	assert.Equal(t, int64(3), rep.Daemon.CurrentJob)
	assert.True(t, rep.DaemonRunning, "heartbeat written at the controller's now")
}
