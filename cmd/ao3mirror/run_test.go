package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A daemon starting while a probe (daemonRunning, e.g. from `source add`) briefly holds the
// lock must wait the probe out, not refuse to run; a lock that stays held still refuses.
func TestLockDaemonWaitsOutProbe(t *testing.T) {
	dir := t.TempDir()
	probe, err := flockDaemon(dir, 1)
	require.NoError(t, err)
	go func() {
		time.Sleep(5 * lockRetry)
		_ = unlockDaemon(probe)
	}()
	unlock, err := lockDaemon(dir)
	require.NoError(t, err, "daemon refused to start during a probe")

	running, err := daemonRunning(dir)
	require.NoError(t, err)
	require.True(t, running)
	_, err = lockDaemon(dir)
	require.ErrorIs(t, err, errDaemonLocked, "second daemon got the lock")
	require.NoError(t, unlock())

	running, err = daemonRunning(dir)
	require.NoError(t, err)
	require.False(t, running)
}
