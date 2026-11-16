package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/fakeao3"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/sources"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// An AO3 page with the layout but none of the work structure: fetchable, not parseable.
var brokenWork = fakeao3.Page(`<div id="workskin"><p>synthetic broken work page</p></div>`)

func countRequests(e *env, uri string) int {
	n := 0
	for _, r := range e.srv.Requests() {
		if r.URI == uri {
			n++
		}
	}
	return n
}

func TestWorkParseFailureKeepsRawAndDoesNotLoop(t *testing.T) {
	e := newEnv(t, 3, nil)
	ctx := context.Background()
	id := e.add(e.site.TagPath(), sources.AddOptions{})
	e.drain(50)

	// Work 5001 was mirrored fine. Now AO3 serves a page we cannot parse after an update.
	good, _, _ := e.st.LatestRawPage(ctx, 5001)
	e.srv.Handle("/works/5001", fakeao3.Reply{Body: brokenWork})
	e.site.Update(5001, func(w *fakeao3.SiteWork) { w.Words += 100; w.Revised = time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC) })
	e.clk.Advance(7 * time.Hour)
	e.drain(50)

	if n := countRequests(e, "/works/5001?view_full_work=true"); n != 2 {
		t.Fatalf("work 5001 requested %d times, want 2 (initial + one failed update, no retries)", n)
	}
	jobs, _ := e.st.ListJobs(ctx, store.JobFailed, 0, "work:5001", 5)
	if len(jobs) != 1 || !strings.HasPrefix(jobs[0].LastError, "parse_") || jobs[0].Attempts != 1 {
		t.Fatalf("failed job: %+v", jobs)
	}
	// Good content kept; failed page stored as the newest raw version, good one still there.
	if chs, _ := e.st.ChapterBodies(ctx, 5001); len(chs) == 0 {
		t.Fatal("existing chapter bodies must be kept")
	}
	latest, _, _ := e.st.LatestRawPage(ctx, 5001)
	if string(latest) != brokenWork {
		t.Fatal("failed page must be stored for reparse")
	}
	var versions int
	e.st.Content.QueryRow(`SELECT COUNT(*) FROM raw_pages WHERE work_id = 5001`).Scan(&versions)
	if versions != 2 || len(good) == 0 {
		t.Fatalf("good raw version must survive: versions=%d", versions)
	}
	c, _ := e.st.Counts(ctx)
	if c.ParseFailures != 1 {
		t.Fatalf("parse failures = %d", c.ParseFailures)
	}

	// The next poll sees the same blurb: no re-fetch until the work changes again.
	e.clk.Advance(7 * time.Hour)
	e.drain(50)
	if n := countRequests(e, "/works/5001?view_full_work=true"); n != 2 {
		t.Fatalf("work re-fetched after parse failure: %d requests", n)
	}
	_ = id
}

func TestBackfillBlocksOnParseFailure(t *testing.T) {
	e := newEnv(t, 45, nil) // 3 listing pages
	ctx := context.Background()
	page2 := e.site.TagPath() + "?page=2&work_search%5Bsort_column%5D=kudos_count&work_search%5Bsort_direction%5D=desc"
	e.srv.Handle(page2, fakeao3.Reply{Body: fakeao3.Page(`<h2 class="heading">x</h2><p>synthetic broken listing</p>`)})
	id := e.add(e.site.TagPath(), sources.AddOptions{})
	e.drain(100)
	e.clk.Advance(time.Minute)
	e.drain(100)

	if n := countRequests(e, page2); n != 1 {
		t.Fatalf("broken listing page requested %d times, want 1", n)
	}
	src, _ := e.st.GetSource(ctx, id)
	cur, _ := store.ParseCursor(src.BackfillCursor)
	if cur.NextPage != 2 || cur.BlockedParser != parse.Version || !strings.Contains(src.LastError, "backfill page 2") {
		t.Fatalf("source after failure: cursor=%+v error=%q", cur, src.LastError)
	}

	// A new parser version (simulated) unblocks; with the page fixed, backfill completes.
	cur.BlockedParser = parse.Version - 1
	src.BackfillCursor = cur.JSON()
	e.st.UpdateSourceProgress(ctx, src)
	e.srv.Handle(page2) // remove override: the synthetic site serves it again
	e.drain(200)
	src, _ = e.st.GetSource(ctx, id)
	if src.State != store.SourceDoneBackfill || src.MemberWorks != 45 || src.LastError != "" {
		t.Fatalf("after unblock: state=%s members=%d error=%q", src.State, src.MemberWorks, src.LastError)
	}
}
