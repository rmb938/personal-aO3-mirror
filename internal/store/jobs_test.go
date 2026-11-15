package store

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

func addSource(t *testing.T, s *Store, target string, state SourceState, prio int) int64 {
	t.Helper()
	id, err := s.InsertSource(context.Background(), SourceRow{Kind: "tag", Target: target, FiltersJSON: "{}", Path: "/p",
		State: state, BackfillOrder: "kudos_desc", PollInterval: time.Hour, Priority: prio, TotalWorks: -1, EstRequests: -1, BackfillCursor: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestEnqueueDedupeAndBump(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	id1, created, err := s.EnqueueJob(ctx, Job{Kind: "work", Target: "1", Priority: 4, DedupeKey: "work:1"})
	if err != nil || !created {
		t.Fatal(err, created)
	}
	id2, created, _ := s.EnqueueJob(ctx, Job{Kind: "work", Target: "1", Priority: 1, DedupeKey: "work:1"})
	if created || id2 != id1 {
		t.Fatalf("dedupe: %d %v", id2, created)
	}
	j, _ := s.GetJob(ctx, id1)
	if j.Priority != 1 {
		t.Fatalf("priority not bumped: %d", j.Priority)
	}
	s.FinishJob(ctx, id1, JobDone, "")
	if _, created, _ := s.EnqueueJob(ctx, Job{Kind: "work", Target: "1", Priority: 4, DedupeKey: "work:1"}); !created {
		t.Fatal("finished job must not block a new one")
	}
}

func TestPickOrderFairnessAndPaused(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	a := addSource(t, s, "a", SourceActive, 0)
	b := addSource(t, s, "b", SourceActive, 0)
	p := addSource(t, s, "p", SourcePaused, 0)
	for i := 0; i < 3; i++ {
		for _, src := range []int64{a, b, p} {
			key := strconv.FormatInt(src, 10) + ":" + strconv.Itoa(i)
			s.EnqueueJob(ctx, Job{Kind: "backfill", SourceID: src, Target: strconv.Itoa(i), Priority: 4, DedupeKey: key})
		}
	}
	s.EnqueueJob(ctx, Job{Kind: "work", Target: "9", Priority: 1, DedupeKey: "work:9"})
	s.EnqueueJob(ctx, Job{Kind: "work", Target: "8", Priority: 1, NotBefore: clk.t.Add(time.Hour), DedupeKey: "work:8"})

	var order []string
	for {
		clk.t = clk.t.Add(time.Second) // distinct last_served_ms
		j, err := s.PickJob(ctx, []string{"backfill", "work"}, clk.t)
		if errors.Is(err, ErrNoJob) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		order = append(order, j.Kind+":"+strconv.FormatInt(j.SourceID, 10))
		s.FinishJob(ctx, j.ID, JobDone, "")
	}
	want := []string{"work:0", "backfill:1", "backfill:2", "backfill:1", "backfill:2", "backfill:1", "backfill:2"}
	if len(order) != len(want) {
		t.Fatalf("order %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order %v, want %v", order, want)
		}
	}
	// kinds filter: work 8 is not due yet; once due it is picked.
	clk.t = clk.t.Add(2 * time.Hour)
	if j, err := s.PickJob(ctx, []string{"backfill"}, clk.t); !errors.Is(err, ErrNoJob) {
		t.Fatalf("paused source job picked: %+v", j)
	}
	if j, err := s.PickJob(ctx, []string{"work"}, clk.t); err != nil || j.Target != "8" {
		t.Fatalf("delayed job: %+v %v", j, err)
	}
}

func TestPriorityWithinClass(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	lo := addSource(t, s, "lo", SourceActive, 0)
	hi := addSource(t, s, "hi", SourceActive, 5)
	s.EnqueueJob(ctx, Job{Kind: "backfill", SourceID: lo, Target: "1", Priority: 4, DedupeKey: "lo"})
	s.EnqueueJob(ctx, Job{Kind: "backfill", SourceID: hi, Target: "1", Priority: 4, DedupeKey: "hi"})
	j, _ := s.PickJob(ctx, []string{"backfill"}, clk.t)
	if j.SourceID != hi {
		t.Fatalf("higher source priority should win: %d", j.SourceID)
	}
}

func TestRetryRecoverAndEvents(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	id, _, _ := s.EnqueueJob(ctx, Job{Kind: "work", Target: "1", Priority: 3, MaxAttempts: 2, DedupeKey: "work:1"})
	j, _ := s.PickJob(ctx, []string{"work"}, clk.t)
	st, err := s.RetryJob(ctx, j.ID, clk.t.Add(time.Minute), "unavailable")
	if err != nil || st != JobQueued {
		t.Fatal(st, err)
	}
	if _, err := s.PickJob(ctx, []string{"work"}, clk.t); !errors.Is(err, ErrNoJob) {
		t.Fatal("retry must respect not_before")
	}
	clk.t = clk.t.Add(2 * time.Minute)
	j, _ = s.PickJob(ctx, []string{"work"}, clk.t)
	if st, _ := s.RetryJob(ctx, j.ID, clk.t, "unavailable"); st != JobFailed {
		t.Fatalf("attempts exhausted should fail: %s", st)
	}

	id2, _, _ := s.EnqueueJob(ctx, Job{Kind: "work", Target: "2", Priority: 3, DedupeKey: "work:2"})
	s.PickJob(ctx, []string{"work"}, clk.t)
	if n, _ := s.RecoverRunning(ctx); n != 1 {
		t.Fatalf("recovered %d", n)
	}
	j2, _ := s.GetJob(ctx, id2)
	if j2.State != JobQueued || j2.Attempts != 0 {
		t.Fatalf("recovered job: %+v", j2)
	}
	s.JobEvent(ctx, id, "picked", map[string]any{"class": 3})
	if ev, _ := s.JobEvents(ctx, id); len(ev) != 1 || ev[0].Detail != `{"class":3}` {
		t.Fatalf("events: %+v", ev)
	}
	if err := s.SetDaemonState(ctx, map[string]string{"heartbeat": "x"}); err != nil {
		t.Fatal(err)
	}
	if m, at, _ := s.DaemonState(ctx); m["heartbeat"] != "x" || at.IsZero() {
		t.Fatal(m, at)
	}
}
