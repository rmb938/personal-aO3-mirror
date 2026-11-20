package redact

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var hashRE = regexp.MustCompile(`^h_[0-9a-f]{24}$`)

// fakeClock is a settable clock for rotation tests.
type fakeClock struct{ ns atomic.Int64 }

func newFakeClock() *fakeClock {
	c := &fakeClock{}
	c.ns.Store(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).UnixNano())
	return c
}

func (c *fakeClock) now() time.Time          { return time.Unix(0, c.ns.Load()).UTC() }
func (c *fakeClock) advance(d time.Duration) { c.ns.Add(int64(d)) }

func TestHashFormatAndDeterminism(t *testing.T) {
	h, err := NewHasher(0, nil)
	require.NoError(t, err)
	ep := h.Current()
	a := ep.Hash("SENTINEL-a")
	assert.Regexp(t, hashRE, a)
	assert.Equal(t, a, ep.Hash("SENTINEL-a"), "same epoch, same input")
	assert.NotEqual(t, a, ep.Hash("SENTINEL-b"))
	assert.Regexp(t, hashRE, ep.Hash(""), "empty strings are hashed too")

	h2, err := NewHasher(0, nil)
	require.NoError(t, err)
	assert.NotEqual(t, a, h2.Current().Hash("SENTINEL-a"), "a new process has a new key")
	assert.NotEqual(t, ep.ID(), h2.Current().ID())
	assert.Regexp(t, `^[0-9a-f]{16}$`, ep.ID())
}

func TestHasherRejectsShortRotation(t *testing.T) {
	for _, d := range []time.Duration{-time.Second, time.Second, MinRotation - 1} {
		_, err := NewHasher(d, nil)
		require.Error(t, err, d)
	}
	for _, d := range []time.Duration{0, MinRotation, 24 * time.Hour} {
		_, err := NewHasher(d, nil)
		assert.NoError(t, err, d)
	}
}

func TestHasherRotation(t *testing.T) {
	clk := newFakeClock()
	h, err := NewHasher(time.Hour, clk.now)
	require.NoError(t, err)
	first := h.Current()

	clk.advance(time.Hour - time.Nanosecond)
	assert.Same(t, first, h.Current(), "not expired yet")

	clk.advance(time.Nanosecond)
	second := h.Current()
	require.NotSame(t, first, second, "rotates exactly at expiry")
	assert.NotEqual(t, first.ID(), second.ID())
	assert.NotEqual(t, first.Hash("x"), second.Hash("x"))
	assert.Same(t, second, h.Current())

	// A long gap rotates once, and the next expiry counts from the rotation.
	clk.advance(10 * time.Hour)
	third := h.Current()
	require.NotSame(t, second, third)
	clk.advance(time.Hour - time.Nanosecond)
	assert.Same(t, third, h.Current())
}

func TestHasherNoRotation(t *testing.T) {
	clk := newFakeClock()
	h, err := NewHasher(0, clk.now)
	require.NoError(t, err)
	first := h.Current()
	clk.advance(1000 * time.Hour)
	assert.Same(t, first, h.Current())
}

func TestHasherConcurrentRotation(t *testing.T) {
	clk := newFakeClock()
	h, err := NewHasher(time.Hour, clk.now)
	require.NoError(t, err)
	first := h.Current()
	clk.advance(time.Hour)

	var wg sync.WaitGroup
	got := make([]*Epoch, 32)
	for i := range got {
		wg.Go(func() { got[i] = h.Current() })
	}
	wg.Wait()
	for _, ep := range got {
		assert.NotSame(t, first, ep)
		assert.Same(t, got[0], ep, "concurrent callers see one rotation")
	}
}

func TestEpochNeverPrintsKey(t *testing.T) {
	h, err := NewHasher(0, nil)
	require.NoError(t, err)
	ep := h.Current()
	keyHex := hex.EncodeToString(ep.key)

	outs := []string{
		fmt.Sprint(ep), fmt.Sprintf("%v %+v %#v %s %q %x", ep, ep, ep, ep, ep, ep),
		fmt.Sprintf("%v", *ep), fmt.Sprintf("%+v", []*Epoch{ep}),
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("m", "epoch", ep)
	slog.New(slog.NewTextHandler(&buf, nil)).Info("m", "epoch", ep)
	outs = append(outs, buf.String())
	for _, s := range outs {
		assert.NotContains(t, s, keyHex)
		assert.NotContains(t, s, string(ep.key))
		assert.NotContains(t, s, fmt.Sprint(ep.key))
	}
	assert.Contains(t, buf.String(), ep.ID())
}
