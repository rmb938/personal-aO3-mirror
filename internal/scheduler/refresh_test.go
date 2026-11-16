package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/fakeao3"
	"github.com/rmb938/personal-aO3-mirror/internal/sources"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

func listingRequests(e *env) int {
	n := 0
	for _, r := range e.srv.Requests() {
		if strings.HasPrefix(r.URI, "/tags/") {
			n++
		}
	}
	return n
}

func TestRefreshRequeuesFailedWorksAndPollsNow(t *testing.T) {
	e := newEnv(t, 5, nil)
	ctx := context.Background()
	e.srv.Handle("/works/5002", fakeao3.Reply{Status: 525, Body: fakeao3.Cloudflare525})
	id := e.add(e.site.TagPath(), sources.AddOptions{})
	// Let 5002 exhaust its attempts (backoff between them is skipped by advancing the clock).
	for i := 0; i < 10; i++ {
		e.drain(50)
		e.clk.Advance(3 * time.Hour)
	}
	// Polls every 6h also re-queue it (still unfetched), so there may be several failed jobs.
	if failed, _ := e.st.ListJobs(ctx, store.JobFailed, 0, "work:5002", 5); len(failed) == 0 {
		t.Fatal("expected failed jobs for 5002")
	}
	e.srv.Handle("/works/5002") // AO3 recovered: the synthetic site serves it again

	before := listingRequests(e)
	res, err := e.srcs.Refresh(ctx, id, false)
	if err != nil || res.RequeuedWorks != 1 || !res.PollNow {
		t.Fatalf("refresh: %+v %v", res, err)
	}
	e.drain(50)
	if listingRequests(e) == before {
		t.Fatal("refresh should poll right away")
	}
	if st, _, _ := e.st.WorkStatus(ctx, 5002); st != "ok" {
		t.Fatalf("5002 status %s", st)
	}
	var fetched int64
	e.st.Meta.QueryRow(`SELECT last_fetched FROM works WHERE id = 5002`).Scan(&fetched)
	if fetched == 0 {
		t.Fatal("re-queued work was not fetched")
	}
	// The old failed jobs are now resolved by the later success: refreshing again must not
	// re-fetch the work.
	if res, err := e.srcs.Refresh(ctx, id, false); err != nil || res.RequeuedWorks != 0 {
		t.Fatalf("second refresh re-queued already-fetched works: %+v %v", res, err)
	}
}

func TestRefreshFullAndSet(t *testing.T) {
	e := newEnv(t, 5, nil)
	ctx := context.Background()
	id := e.add(e.site.TagPath(), sources.AddOptions{})
	e.drain(50)
	if src, _ := e.st.GetSource(ctx, id); src.State != store.SourceDoneBackfill {
		t.Fatalf("state %s", src.State)
	}
	res, err := e.srcs.Refresh(ctx, id, true)
	if err != nil || !res.RescanStarted {
		t.Fatalf("full refresh: %+v %v", res, err)
	}
	if src, _ := e.st.GetSource(ctx, id); src.State != store.SourceActive {
		t.Fatalf("full refresh should reactivate: %s", src.State)
	}
	e.drain(50)
	src, _ := e.st.GetSource(ctx, id)
	cur, _ := store.ParseCursor(src.BackfillCursor)
	if src.State != store.SourceDoneBackfill || cur.Rescan {
		t.Fatalf("after full refresh: %s %+v", src.State, cur)
	}

	// set: shorter poll interval moves the next poll earlier; priority changes.
	prio := 7
	if err := e.srcs.Set(ctx, id, 30*time.Minute, &prio); err != nil {
		t.Fatal(err)
	}
	src, _ = e.st.GetSource(ctx, id)
	if src.PollInterval != 30*time.Minute || src.Priority != 7 || src.NextPollAt.After(e.clk.Now().Add(31*time.Minute)) {
		t.Fatalf("set: interval=%s prio=%d next=%s", src.PollInterval, src.Priority, src.NextPollAt)
	}
	if err := e.srcs.Set(ctx, id, time.Minute, nil); err == nil {
		t.Fatal("poll interval below 10m must be rejected")
	}
	e.srcs.Pause(ctx, id)
	if _, err := e.srcs.Refresh(ctx, id, false); err == nil {
		t.Fatal("refresh of a paused source must fail")
	}
}
