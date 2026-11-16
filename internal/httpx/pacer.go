package httpx

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/config"
)

// Clock abstracts time so tests can run pacing logic without real sleeps.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Backoff tuning. Kept as package vars so they are visible in one place.
var (
	defaultRetryAfter   = 15 * time.Minute // 429 without Retry-After
	slowdownFactor      = 1.5              // interval multiplier after a 429
	maxSlowdown         = 8.0              // cap on the cumulative multiplier
	slowdownDuration    = 24 * time.Hour   // how long a slowdown lasts
	unavailableBase     = 1 * time.Minute  // first backoff on 5xx/Cloudflare
	unavailableMax      = 1 * time.Hour    // cap on exponential backoff
	breakerThreshold    = 5                // consecutive failures that open the breaker
	breakerOpenDuration = 2 * time.Hour
)

// PacerState is a snapshot for logging and `status`.
type PacerState struct {
	Interval    time.Duration // current base interval incl. slowdown, before jitter
	Factor      float64
	SlowUntil   time.Time
	BackoffTill time.Time
	ConsecFails int
	BreakerOpen bool
	DayCount    int
	DailyBudget int
	// WaitingUntil is set while a request is waiting for its slot (interval, backoff, breaker
	// or daily budget); zero when not waiting.
	WaitingUntil time.Time
}

// Pacer enforces one-at-a-time spacing, jitter, the hard floor, backoff and the daily budget.
type Pacer struct {
	mu           sync.Mutex
	clock        Clock
	base         time.Duration
	jitter       float64
	budget       int
	rnd          *rand.Rand
	last         time.Time
	backoffTill  time.Time
	factor       float64
	slowUntil    time.Time
	consecFails  int
	breakerTill  time.Time
	day          time.Time
	dayCount     int
	waitingUntil time.Time
}

func NewPacer(clock Clock, base time.Duration, jitter float64, dailyBudget int, seed uint64) *Pacer {
	if base < config.MinInterval {
		base = config.MinInterval
	}
	return &Pacer{
		clock: clock, base: base, jitter: jitter, budget: dailyBudget,
		rnd: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), factor: 1,
	}
}

func (p *Pacer) currentFactor(now time.Time) float64 {
	if now.After(p.slowUntil) {
		p.factor = 1
	}
	return p.factor
}

// nextInterval returns the jittered gap; never below config.MinInterval.
func (p *Pacer) nextInterval(now time.Time) time.Duration {
	d := float64(p.base) * p.currentFactor(now)
	d *= 1 + p.jitter*(2*p.rnd.Float64()-1)
	if iv := time.Duration(d); iv > config.MinInterval {
		return iv
	}
	return config.MinInterval
}

// Wait blocks until the next request may be sent and reserves that slot.
// It returns how long it waited.
//
// Requests are serialized by the Client's own lock, so the pacer lock is released while
// sleeping; that keeps State() (heartbeat, status) readable during long backoffs.
func (p *Pacer) Wait(ctx context.Context) (time.Duration, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	start := p.clock.Now()
	gap := p.nextInterval(start) // jitter drawn once per request
	defer func() { p.waitingUntil = time.Time{} }()
	for {
		now := p.clock.Now()
		next := now
		if !p.last.IsZero() {
			next = p.last.Add(gap)
		}
		for _, t := range []time.Time{p.backoffTill, p.breakerTill} {
			if t.After(next) {
				next = t
			}
		}
		if p.budget > 0 {
			p.rollDay(now)
			if p.dayCount >= p.budget {
				if t := p.day.Add(24 * time.Hour); t.After(next) {
					next = t
				}
			}
		}
		d := next.Sub(now)
		if d <= 0 {
			break
		}
		p.waitingUntil = next
		p.mu.Unlock()
		err := p.clock.Sleep(ctx, d)
		p.mu.Lock()
		if err != nil {
			return p.clock.Now().Sub(start), err
		}
	}
	now := p.clock.Now()
	p.last = now
	if p.budget > 0 {
		p.rollDay(now)
		p.dayCount++
	}
	return now.Sub(start), nil
}

func (p *Pacer) rollDay(now time.Time) {
	if day := now.UTC().Truncate(24 * time.Hour); !day.Equal(p.day) {
		p.day, p.dayCount = day, 0
	}
}

// Report feeds the outcome of the last request back into the pacer.
func (p *Pacer) Report(class Class, retryAfter time.Duration) PacerState {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock.Now()
	switch class {
	case ClassRateLimited:
		p.consecFails++
		d := retryAfter
		if d <= 0 {
			d = defaultRetryAfter
		}
		p.setBackoff(now.Add(d))
		f := p.currentFactor(now) * slowdownFactor
		if f > maxSlowdown {
			f = maxSlowdown
		}
		p.factor, p.slowUntil = f, now.Add(slowdownDuration)
	case ClassUnavailable:
		p.consecFails++
		d := unavailableBase << (p.consecFails - 1)
		if d > unavailableMax || d <= 0 {
			d = unavailableMax
		}
		if retryAfter > d {
			d = retryAfter
		}
		p.setBackoff(now.Add(d))
	default:
		p.consecFails = 0
	}
	if p.consecFails >= breakerThreshold {
		p.breakerTill = now.Add(breakerOpenDuration)
	}
	return p.stateLocked(now)
}

func (p *Pacer) setBackoff(t time.Time) {
	if t.After(p.backoffTill) {
		p.backoffTill = t
	}
}

func (p *Pacer) State() PacerState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stateLocked(p.clock.Now())
}

func (p *Pacer) stateLocked(now time.Time) PacerState {
	f := p.currentFactor(now)
	return PacerState{
		Interval:     time.Duration(float64(p.base) * f),
		Factor:       f,
		SlowUntil:    p.slowUntil,
		BackoffTill:  p.backoffTill,
		ConsecFails:  p.consecFails,
		BreakerOpen:  now.Before(p.breakerTill),
		DayCount:     p.dayCount,
		DailyBudget:  p.budget,
		WaitingUntil: p.waitingUntil,
	}
}
