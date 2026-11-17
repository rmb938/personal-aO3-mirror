package store

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func addSource(t *testing.T, s *testStore, target string, state SourceState, prio int) int64 {
	t.Helper()
	id, err := s.InsertSource(context.Background(), SourceRow{Kind: "tag", Target: target, FiltersJSON: "{}", Path: "/p",
		State: state, BackfillOrder: "kudos_desc", PollInterval: time.Hour, Priority: prio, TotalWorks: -1, EstRequests: -1, BackfillCursor: "{}"})
	require.NoError(t, err)
	return id
}

// enqueue adds a job, failing the test on error, and returns its id.
func enqueue(t *testing.T, s *testStore, j Job) int64 {
	t.Helper()
	id, _, err := s.EnqueueJob(context.Background(), j)
	require.NoError(t, err)
	return id
}

func TestEnqueueDedupeAndBump(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	id1, created, err := s.EnqueueJob(ctx, Job{Kind: "work", Target: "1", Priority: 4, DedupeKey: "work:1"})
	require.NoError(t, err)
	require.True(t, created)
	id2, created, err := s.EnqueueJob(ctx, Job{Kind: "work", Target: "1", Priority: 1, DedupeKey: "work:1"})
	require.NoError(t, err)
	require.False(t, created, "dedupe")
	require.Equal(t, id1, id2, "dedupe returns the existing job")
	j, err := s.GetJob(ctx, id1)
	require.NoError(t, err)
	require.Equal(t, 1, j.Priority, "priority bumped")
	require.NoError(t, s.FinishJob(ctx, id1, JobDone, ""))
	_, created, err = s.EnqueueJob(ctx, Job{Kind: "work", Target: "1", Priority: 4, DedupeKey: "work:1"})
	require.NoError(t, err)
	require.True(t, created, "finished job must not block a new one")
}

func TestPickOrderFairnessAndPaused(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	a := addSource(t, s, "a", SourceActive, 0)
	b := addSource(t, s, "b", SourceActive, 0)
	p := addSource(t, s, "p", SourcePaused, 0)
	for i := range 3 {
		for _, src := range []int64{a, b, p} {
			key := strconv.FormatInt(src, 10) + ":" + strconv.Itoa(i)
			enqueue(t, s, Job{Kind: "backfill", SourceID: src, Target: strconv.Itoa(i), Priority: 4, DedupeKey: key})
		}
	}
	enqueue(t, s, Job{Kind: "work", Target: "9", Priority: 1, DedupeKey: "work:9"})
	enqueue(t, s, Job{Kind: "work", Target: "8", Priority: 1, NotBefore: clk.t.Add(time.Hour), DedupeKey: "work:8"})

	var order []string
	for {
		clk.t = clk.t.Add(time.Second) // distinct last_served_ms
		j, err := s.PickJob(ctx, []string{"backfill", "work"}, clk.t)
		if errors.Is(err, ErrNoJob) {
			break
		}
		require.NoError(t, err)
		order = append(order, j.Kind+":"+strconv.FormatInt(j.SourceID, 10))
		require.NoError(t, s.FinishJob(ctx, j.ID, JobDone, ""))
	}
	require.Equal(t, []string{"work:0", "backfill:1", "backfill:2", "backfill:1", "backfill:2", "backfill:1", "backfill:2"}, order)

	// kinds filter: work 8 is not due yet; once due it is picked.
	clk.t = clk.t.Add(2 * time.Hour)
	_, err := s.PickJob(ctx, []string{"backfill"}, clk.t)
	require.ErrorIs(t, err, ErrNoJob, "paused source job must not be picked")
	j, err := s.PickJob(ctx, []string{"work"}, clk.t)
	require.NoError(t, err)
	require.Equal(t, "8", j.Target, "delayed job")
}

func TestPriorityWithinClass(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	lo := addSource(t, s, "lo", SourceActive, 0)
	hi := addSource(t, s, "hi", SourceActive, 5)
	enqueue(t, s, Job{Kind: "backfill", SourceID: lo, Target: "1", Priority: 4, DedupeKey: "lo"})
	enqueue(t, s, Job{Kind: "backfill", SourceID: hi, Target: "1", Priority: 4, DedupeKey: "hi"})
	j, err := s.PickJob(ctx, []string{"backfill"}, clk.t)
	require.NoError(t, err)
	require.Equal(t, hi, j.SourceID, "higher source priority should win")
}

func TestRetryRecoverAndEvents(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	id := enqueue(t, s, Job{Kind: "work", Target: "1", Priority: 3, MaxAttempts: 2, DedupeKey: "work:1"})
	j, err := s.PickJob(ctx, []string{"work"}, clk.t)
	require.NoError(t, err)
	st, err := s.RetryJob(ctx, j.ID, clk.t.Add(time.Minute), "unavailable")
	require.NoError(t, err)
	require.Equal(t, JobQueued, st)
	_, err = s.PickJob(ctx, []string{"work"}, clk.t)
	require.ErrorIs(t, err, ErrNoJob, "retry must respect not_before")
	clk.t = clk.t.Add(2 * time.Minute)
	j, err = s.PickJob(ctx, []string{"work"}, clk.t)
	require.NoError(t, err)
	st, err = s.RetryJob(ctx, j.ID, clk.t, "unavailable")
	require.NoError(t, err)
	require.Equal(t, JobFailed, st, "attempts exhausted should fail")

	id2 := enqueue(t, s, Job{Kind: "work", Target: "2", Priority: 3, DedupeKey: "work:2"})
	_, err = s.PickJob(ctx, []string{"work"}, clk.t)
	require.NoError(t, err)
	n, err := s.RecoverRunning(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "recovered")
	j2, err := s.GetJob(ctx, id2)
	require.NoError(t, err)
	require.Equal(t, JobQueued, j2.State)
	require.Equal(t, 1, j2.Attempts, "a crash counts as an attempt")
	require.Equal(t, CodeCrashed, j2.LastError)

	// A job that keeps killing the daemon runs out of attempts instead of crash-looping.
	id3 := enqueue(t, s, Job{Kind: "work", Target: "3", Priority: 1, MaxAttempts: 2, DedupeKey: "work:3"})
	for range 2 {
		j, err := s.PickJob(ctx, []string{"work"}, clk.t)
		require.NoError(t, err)
		require.Equal(t, id3, j.ID)
		_, err = s.RecoverRunning(ctx)
		require.NoError(t, err)
	}
	j3, err := s.GetJob(ctx, id3)
	require.NoError(t, err)
	require.Equal(t, JobFailed, j3.State, "crash loop must end")
	require.Equal(t, CodeCrashed, j3.LastError)

	require.NoError(t, s.JobEvent(ctx, id, "picked", map[string]any{"class": 3}))
	ev, err := s.JobEvents(ctx, id)
	require.NoError(t, err)
	require.Len(t, ev, 1)
	require.JSONEq(t, `{"class":3}`, ev[0].Detail)
	require.NoError(t, s.SetDaemonState(ctx, map[string]string{"heartbeat": "x"}))
	m, at, err := s.DaemonState(ctx)
	require.NoError(t, err)
	require.Equal(t, "x", m["heartbeat"])
	require.False(t, at.IsZero())
}
