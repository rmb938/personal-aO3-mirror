package httpx

import (
	"context"
	"sync"
	"testing"
	"time"
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
	p := NewPacer(clk, 10*time.Second, 0, 0, 1)
	ctx := context.Background()
	p.Wait(ctx) // first request: no wait
	sent := clk.now
	clk.now = clk.now.Add(4 * time.Second) // slow response: body read 4s after sending
	p.Report(ClassOK, 0)
	p.Wait(ctx)
	if gap := clk.now.Sub(sent); gap != 14*time.Second {
		t.Fatalf("next request %s after the previous was sent; want 14s (4s response + 10s gap)", gap)
	}
}

func TestStateReadableDuringWait(t *testing.T) {
	clk := &blockingClock{now: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC), sleep: make(chan struct{}), release: make(chan struct{})}
	p := NewPacer(clk, 10*time.Second, 0, 0, 1)
	p.Report(ClassUnavailable, 0) // 1 minute backoff
	done := make(chan struct{})
	go func() { p.Wait(context.Background()); close(done) }()
	<-clk.sleep // Wait is now sleeping

	got := make(chan PacerState)
	go func() { got <- p.State() }()
	select {
	case st := <-got:
		if st.WaitingUntil.IsZero() || st.WaitingUntil.Sub(clk.Now()) != time.Minute {
			t.Fatalf("WaitingUntil = %v (now %v)", st.WaitingUntil, clk.Now())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("State() blocked while Wait was sleeping")
	}
	close(clk.release)
	<-done
	if st := p.State(); !st.WaitingUntil.IsZero() {
		t.Fatalf("WaitingUntil should clear after Wait: %v", st.WaitingUntil)
	}
}
