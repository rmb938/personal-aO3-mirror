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
