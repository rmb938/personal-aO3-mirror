package httpx

import (
	"context"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// blockingClock's Sleep blocks until released, like a long real backoff.
type blockingClock struct {
	mu      sync.Mutex
	now     time.Time
	sleep   chan struct{}
	release chan struct{}
}

func (c *blockingClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }

func (c *blockingClock) Sleep(ctx context.Context, d time.Duration) error {
	c.sleep <- struct{}{}
	<-c.release
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
	return ctx.Err()
}

// stepClock advances only when slept on (or when the test moves it).
type stepClock struct{ now time.Time }

func (c *stepClock) Now() time.Time { return c.now }
func (c *stepClock) Sleep(ctx context.Context, d time.Duration) error {
	c.now = c.now.Add(d)
	return ctx.Err()
}

func TestIntervalCountsFromResponseEnd(t *testing.T) {
	clk := &stepClock{now: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)}
	p := NewPacer(clk, 10*time.Second, 0, 0, rand.NewPCG(1, 1))
	ctx := context.Background()
	_, err := p.Wait(ctx) // first request: no wait
	require.NoError(t, err)
	sent := clk.now
	clk.now = clk.now.Add(4 * time.Second) // slow response: body read 4s after sending
	p.Report(ClassOK, 0)
	_, err = p.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, 14*time.Second, clk.now.Sub(sent), "next request 4s response + 10s gap after the previous was sent")
}

func TestStateReadableDuringWait(t *testing.T) {
	clk := &blockingClock{now: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC), sleep: make(chan struct{}), release: make(chan struct{})}
	p := NewPacer(clk, 10*time.Second, 0, 0, rand.NewPCG(1, 1))
	p.Report(ClassUnavailable, 0) // 1 minute backoff
	waitErr := make(chan error, 1)
	go func() {
		_, err := p.Wait(context.Background())
		waitErr <- err
	}()
	<-clk.sleep // Wait is now sleeping

	got := make(chan PacerState)
	go func() { got <- p.State() }()
	select {
	case st := <-got:
		require.False(t, st.WaitingUntil.IsZero(), "WaitingUntil should be set while waiting")
		require.Equal(t, time.Minute, st.WaitingUntil.Sub(clk.Now()))
	case <-time.After(2 * time.Second):
		require.FailNow(t, "State() blocked while Wait was sleeping")
	}
	close(clk.release)
	require.NoError(t, <-waitErr)
	require.True(t, p.State().WaitingUntil.IsZero(), "WaitingUntil should clear after Wait")
}
