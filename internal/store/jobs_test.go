package store

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

func addSource(tb testing.TB, s *testStore, target string, state model.SourceState, prio int) int64 {
	tb.Helper()
	id, err := s.InsertSource(context.Background(), model.Source{Kind: "tag", Target: target, FiltersJSON: "{}", Path: "/p",
		State: state, BackfillOrder: "kudos_desc", PollInterval: time.Hour, Priority: prio, TotalWorks: -1, EstRequests: -1, BackfillCursor: "{}"})
	require.NoError(tb, err)
	return id
}

// enqueue adds a job, failing the test on error, and returns its id.
func enqueue(t *testing.T, s *testStore, j NewJob) int64 {
	t.Helper()
	id, _, err := s.EnqueueJob(context.Background(), j)
	require.NoError(t, err)
	return id
}

// A work fetch queued by a paused source must not swallow an on-demand request for that work.
func TestEnqueueTakesOverParkedJob(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	paused := addSource(t, s, "paused", model.SourcePaused, 0)
	id := enqueue(t, s, NewJob{Kind: "work", SourceID: paused, Target: "42", Priority: 4, DedupeKey: "work:42"})
	_, err := s.PickJob(ctx, []string{"work"}, clk.t)
	require.ErrorIs(t, err, ErrNoJob, "paused source's job must not run")

	got, created, err := s.EnqueueJob(ctx, NewJob{Kind: "work", Target: "42", Priority: 1, DedupeKey: "work:42"})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, id, got)
	j, err := s.PickJob(ctx, []string{"work"}, clk.t)
	require.NoError(t, err, "taken-over job should run")
	require.Equal(t, id, j.ID)
	require.Zero(t, j.SourceID)
	require.Equal(t, 1, j.Priority)
}

func TestEnqueueDedupeAndBump(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	id1, created, err := s.EnqueueJob(ctx, NewJob{Kind: "work", Target: "1", Priority: 4, DedupeKey: "work:1"})
	require.NoError(t, err)
	require.True(t, created)
	id2, created, err := s.EnqueueJob(ctx, NewJob{Kind: "work", Target: "1", Priority: 1, DedupeKey: "work:1"})
	require.NoError(t, err)
	require.False(t, created, "dedupe")
	require.Equal(t, id1, id2, "dedupe returns the existing job")
	j, err := s.GetJob(ctx, id1)
	require.NoError(t, err)
	require.Equal(t, 1, j.Priority, "priority bumped")
	require.NoError(t, s.FinishJob(ctx, id1, model.JobDone, ""))
	_, created, err = s.EnqueueJob(ctx, NewJob{Kind: "work", Target: "1", Priority: 4, DedupeKey: "work:1"})
	require.NoError(t, err)
	require.True(t, created, "finished job must not block a new one")
}

func TestPickOrderFairnessAndPaused(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	a := addSource(t, s, "a", model.SourceActive, 0)
	b := addSource(t, s, "b", model.SourceActive, 0)
	p := addSource(t, s, "p", model.SourcePaused, 0)
	// Work 8 is retried an hour out before the others are queued, so it is not due yet.
	delayed := enqueue(t, s, NewJob{Kind: "work", Target: "8", Priority: 1, DedupeKey: "work:8"})
	j, err := s.PickJob(ctx, []string{"work"}, clk.t)
	require.NoError(t, err)
	require.Equal(t, delayed, j.ID)
	_, err = s.RetryJob(ctx, delayed, clk.t.Add(time.Hour), "unavailable")
	require.NoError(t, err)
	for i := range 3 {
		for _, src := range []int64{a, b, p} {
			key := strconv.FormatInt(src, 10) + ":" + strconv.Itoa(i)
			enqueue(t, s, NewJob{Kind: "backfill", SourceID: src, Target: strconv.Itoa(i), Priority: 4, DedupeKey: key})
		}
	}
	enqueue(t, s, NewJob{Kind: "work", Target: "9", Priority: 1, DedupeKey: "work:9"})

	var order []string
	for {
		clk.t = clk.t.Add(time.Second) // distinct last_served_ms
		j, err := s.PickJob(ctx, []string{"backfill", "work"}, clk.t)
		if errors.Is(err, ErrNoJob) {
			break
		}
		require.NoError(t, err)
		order = append(order, j.Kind+":"+strconv.FormatInt(j.SourceID, 10))
		require.NoError(t, s.FinishJob(ctx, j.ID, model.JobDone, ""))
	}
	require.Equal(t, []string{"work:0", "backfill:1", "backfill:2", "backfill:1", "backfill:2", "backfill:1", "backfill:2"}, order)

	// kinds filter: work 8 is not due yet; once due it is picked.
	clk.t = clk.t.Add(2 * time.Hour)
	_, err = s.PickJob(ctx, []string{"backfill"}, clk.t)
	require.ErrorIs(t, err, ErrNoJob, "paused source job must not be picked")
	j, err = s.PickJob(ctx, []string{"work"}, clk.t)
	require.NoError(t, err)
	require.Equal(t, "8", j.Target, "delayed job")
}

func TestPriorityWithinClass(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	lo := addSource(t, s, "lo", model.SourceActive, 0)
	hi := addSource(t, s, "hi", model.SourceActive, 5)
	enqueue(t, s, NewJob{Kind: "backfill", SourceID: lo, Target: "1", Priority: 4, DedupeKey: "lo"})
	enqueue(t, s, NewJob{Kind: "backfill", SourceID: hi, Target: "1", Priority: 4, DedupeKey: "hi"})
	j, err := s.PickJob(ctx, []string{"backfill"}, clk.t)
	require.NoError(t, err)
	require.Equal(t, hi, j.SourceID, "higher source priority should win")
}

func TestRetryRecoverAndEvents(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	id := enqueue(t, s, NewJob{Kind: "work", Target: "1", Priority: 3, DedupeKey: "work:1"})
	j, err := s.PickJob(ctx, []string{"work"}, clk.t)
	require.NoError(t, err)
	st, err := s.RetryJob(ctx, j.ID, clk.t.Add(time.Minute), "unavailable")
	require.NoError(t, err)
	require.Equal(t, model.JobQueued, st)
	_, err = s.PickJob(ctx, []string{"work"}, clk.t)
	require.ErrorIs(t, err, ErrNoJob, "retry must respect not_before")
	clk.t = clk.t.Add(2 * time.Minute)
	for attempt := 2; attempt <= DefaultMaxAttempts; attempt++ {
		j, err = s.PickJob(ctx, []string{"work"}, clk.t)
		require.NoError(t, err)
		st, err = s.RetryJob(ctx, j.ID, clk.t, "unavailable")
		require.NoError(t, err)
		want := model.JobQueued
		if attempt == DefaultMaxAttempts {
			want = model.JobFailed // attempts exhausted
		}
		require.Equal(t, want, st, "attempt %d", attempt)
	}

	id2 := enqueue(t, s, NewJob{Kind: "work", Target: "2", Priority: 3, DedupeKey: "work:2"})
	_, err = s.PickJob(ctx, []string{"work"}, clk.t)
	require.NoError(t, err)
	n, err := s.RecoverRunning(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "recovered")
	j2, err := s.GetJob(ctx, id2)
	require.NoError(t, err)
	require.Equal(t, model.JobQueued, j2.State)
	require.Equal(t, 1, j2.Attempts, "a crash counts as an attempt")
	require.Equal(t, CodeCrashed, j2.LastError)

	// A job that keeps killing the daemon runs out of attempts instead of crash-looping.
	id3 := enqueue(t, s, NewJob{Kind: "work", Target: "3", Priority: 1, DedupeKey: "work:3"})
	for range DefaultMaxAttempts {
		j, err := s.PickJob(ctx, []string{"work"}, clk.t)
		require.NoError(t, err)
		require.Equal(t, id3, j.ID)
		_, err = s.RecoverRunning(ctx)
		require.NoError(t, err)
	}
	j3, err := s.GetJob(ctx, id3)
	require.NoError(t, err)
	require.Equal(t, model.JobFailed, j3.State, "crash loop must end")
	require.Equal(t, CodeCrashed, j3.LastError)

	require.NoError(t, s.JobEvent(ctx, id, "picked", map[string]any{"class": 3}))
	ev, err := s.JobEvents(ctx, id)
	require.NoError(t, err)
	require.Len(t, ev, 1)
	require.JSONEq(t, `{"class":3}`, ev[0].Detail)
}

func TestFindJobsPagedAndCount(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	src := addSource(t, s, "Fixture Tag", model.SourceActive, 0)
	var ids []int64
	for i := range 7 {
		j := NewJob{Kind: "work", Target: strconv.Itoa(i), Priority: 2, DedupeKey: "work:" + strconv.Itoa(i)}
		if i%2 == 0 {
			j.Kind, j.SourceID = "poll", src
		}
		ids = append(ids, enqueue(t, s, j))
	}
	require.NoError(t, s.FinishJob(ctx, ids[0], model.JobDone, ""))
	require.NoError(t, s.FinishJob(ctx, ids[1], model.JobFailed, "not_found"))

	for _, c := range []struct {
		f    model.JobFilter
		want []int64 // newest first
	}{
		{model.JobFilter{}, []int64{ids[6], ids[5], ids[4], ids[3], ids[2], ids[1], ids[0]}},
		{model.JobFilter{State: model.JobQueued}, []int64{ids[6], ids[5], ids[4], ids[3], ids[2]}},
		{model.JobFilter{Kind: "poll"}, []int64{ids[6], ids[4], ids[2], ids[0]}},
		{model.JobFilter{SourceID: src, State: model.JobQueued}, []int64{ids[6], ids[4], ids[2]}},
		{model.JobFilter{Kind: "work", State: model.JobFailed}, []int64{ids[1]}},
		{model.JobFilter{SourceID: src + 1}, nil},
	} {
		n, err := s.CountJobs(ctx, c.f)
		require.NoError(t, err)
		require.Equal(t, len(c.want), n, "%+v", c.f)
		var got []int64
		for offset := 0; offset < n; offset += 2 {
			f := c.f
			f.Offset, f.Limit = offset, 2
			page, err := s.FindJobs(ctx, f)
			require.NoError(t, err)
			for _, j := range page {
				got = append(got, j.ID)
			}
		}
		require.Equal(t, c.want, got, "%+v: pages cover every match once, newest first", c.f)
		f := c.f
		f.Offset, f.Limit = n, 2
		page, err := s.FindJobs(ctx, f)
		require.NoError(t, err)
		require.Empty(t, page, "%+v: offset past the end", c.f)
	}
	for _, bad := range [][2]int{{-1, 10}, {0, 0}, {0, -1}} {
		_, err := s.FindJobs(ctx, model.JobFilter{Offset: bad[0], Limit: bad[1]})
		require.Error(t, err, "offset %d limit %d", bad[0], bad[1])
	}
}
