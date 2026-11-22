package auth

import (
	"context"
	"runtime"
	"runtime/debug"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHasherFlood is a login flood on the default Hasher (docs/part2-api.md, stage 17b): more
// verifies at once than its concurrency cap, at the real 64 MiB memory cost. At most 2 hashes
// run, so the heap grows by about 3 × 64 MiB (the 2 running and one finished, not yet
// collected), not floodN × 64 MiB (measured: 192 vs 512 with the cap at floodN); and a waiter
// whose request deadline passes leaves at once rather than at MaxWait.
func TestHasherFlood(t *testing.T) {
	const (
		floodN = 8
		mib    = 1 << 20
	)
	// DefaultArgon2's memory; one pass and one lane keep it quick under -race.
	p := Argon2Params{Memory: DefaultArgon2.Memory, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}
	h, err := NewHasher(HasherOptions{Params: p, MaxWait: time.Minute})
	require.NoError(t, err)
	require.Equal(t, 2, cap(h.sem), "the default cap")
	ctx := t.Context()
	phc, err := h.Hash(ctx, "fixture password")
	require.NoError(t, err)

	// A low GC target, so finished hashes' memory is collected soon and the peak shows what
	// is live.
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)

	var peakHeap uint64
	var peakSlots int
	stop := make(chan struct{})
	var sampler sync.WaitGroup
	sampler.Go(func() {
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			default:
			}
			runtime.ReadMemStats(&m)
			peakHeap = max(peakHeap, m.HeapAlloc)
			peakSlots = max(peakSlots, len(h.sem))
			time.Sleep(time.Millisecond)
		}
	})

	start := make(chan struct{})
	oks := make([]bool, floodN)
	errs := make([]error, floodN)
	var flood sync.WaitGroup
	for i := range floodN {
		flood.Go(func() {
			<-start
			oks[i], errs[i] = h.Verify(ctx, phc, "fixture password")
		})
	}
	close(start)
	require.Eventually(t, func() bool { return len(h.sem) == cap(h.sem) }, 10*time.Second, time.Millisecond)

	// Both slots are held and the queue is long: a request deadline ends the wait.
	dctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	began := time.Now()
	_, err = h.Verify(dctx, phc, "fixture password")
	cancel()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(began), 5*time.Second, "left at its deadline, not at MaxWait")

	flood.Wait()
	close(stop)
	sampler.Wait()
	for i := range floodN {
		require.NoError(t, errs[i], "verify %d", i)
		assert.True(t, oks[i], "verify %d", i)
	}
	assert.Equal(t, cap(h.sem), peakSlots, "the flood filled both slots, never more")
	require.Greater(t, peakHeap, base.HeapAlloc, "sampled during the flood")
	grown := (peakHeap - base.HeapAlloc) / mib
	t.Logf("heap grew by %d MiB at peak (%d verifies of %d MiB each)", grown, floodN, p.Memory/1024)
	assert.Less(t, grown, uint64(4*p.Memory/1024), "about 2 hashes' memory, not %d", floodN)
}
