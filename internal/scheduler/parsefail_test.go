package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/fakeao3"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
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
	e.add(e.site.TagPath())
	e.drain(50)

	// Work 5001 was mirrored fine. Now AO3 serves a page we cannot parse after an update.
	good, _, err := e.db.LatestRawPage(ctx, 5001)
	require.NoError(t, err)
	e.srv.Handle("/works/5001", fakeao3.Reply{Body: brokenWork})
	e.site.Update(5001, func(w *fakeao3.SiteWork) { w.Words += 100; w.Revised = time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC) })
	e.clk.Advance(7 * time.Hour)
	e.drain(50)

	require.Equal(t, 2, countRequests(e, "/works/5001?view_full_work=true"), "initial + one failed update, no retries")
	jobs, err := e.db.ListJobs(ctx, store.JobFailed, 0, "work:5001", 5)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Contains(t, jobs[0].LastError, "parse_")
	require.Equal(t, 1, jobs[0].Attempts)
	// Good content kept; failed page stored as the newest raw version, good one still there.
	chs, err := e.db.ChapterBodies(ctx, 5001)
	require.NoError(t, err)
	require.NotEmpty(t, chs, "existing chapter bodies must be kept")
	latest, _, err := e.db.LatestRawPage(ctx, 5001)
	require.NoError(t, err)
	require.Equal(t, brokenWork, string(latest), "failed page must be stored for reparse")
	require.Equal(t, 2, e.diag(5001).RawVersions, "good raw version must survive")
	require.NotEmpty(t, good)
	c, err := e.db.Counts(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, c.ParseFailures)

	// The next poll sees the same blurb: no re-fetch until the work changes again.
	e.clk.Advance(7 * time.Hour)
	e.drain(50)
	require.Equal(t, 2, countRequests(e, "/works/5001?view_full_work=true"), "work re-fetched after parse failure")
}

func TestBackfillBlocksOnParseFailure(t *testing.T) {
	e := newEnv(t, 45, nil) // 3 listing pages
	ctx := context.Background()
	page2 := e.site.TagPath() + "?page=2&work_search%5Bsort_column%5D=kudos_count&work_search%5Bsort_direction%5D=desc"
	e.srv.Handle(page2, fakeao3.Reply{Body: fakeao3.Page(`<h2 class="heading">x</h2><p>synthetic broken listing</p>`)})
	id := e.add(e.site.TagPath())
	e.drain(100)
	e.clk.Advance(time.Minute)
	e.drain(100)

	require.Equal(t, 1, countRequests(e, page2), "broken listing page requested once")
	src := e.source(id)
	cur, err := controllers.ParseCursor(src.BackfillCursor)
	require.NoError(t, err)
	require.Equal(t, 2, cur.NextPage)
	require.Equal(t, parse.Version, cur.BlockedParser)
	require.Contains(t, src.LastError, "backfill page 2")

	// A new parser version (simulated) unblocks; with the page fixed, backfill completes.
	cur.BlockedParser = parse.Version - 1
	src.BackfillCursor = cur.JSON()
	require.NoError(t, e.db.UpdateSourceProgress(ctx, src))
	e.srv.Handle(page2) // remove override: the synthetic site serves it again
	e.drain(200)
	src = e.source(id)
	require.Equal(t, store.SourceDoneBackfill, src.State, "after unblock")
	require.Equal(t, 45, src.MemberWorks)
	require.Empty(t, src.LastError)
}
