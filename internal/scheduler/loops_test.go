package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/fakeao3"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// These tests guard against request loops: outcomes that used to re-queue the same AO3 fetch
// every scheduler round (or every minute) instead of waiting.

// A single-work source whose work is gone, hidden or unparseable is fetched once per poll
// interval, not every round.
func TestWorkSourceUnavailableDoesNotLoop(t *testing.T) {
	cases := map[string]func(e *env){
		"deleted":     func(e *env) { e.site.Update(5002, func(w *fakeao3.SiteWork) { w.Deleted = true }) },
		"restricted":  func(e *env) { e.srv.Handle("/works/5002", restrictedRedirect("/works/5002")) },
		"unparseable": func(e *env) { e.srv.Handle("/works/5002", fakeao3.Reply{Body: brokenWork}) },
		"unavailable": func(e *env) {
			e.srv.Handle("/works/5002", fakeao3.Reply{Status: 525, Body: fakeao3.Cloudflare525})
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, 3, nil)
			setup(e)
			e.add("/works/5002")
			const uri = "/works/5002?view_full_work=true"
			e.drain(10)
			first := countRequests(e, uri)
			require.NotZero(t, first)
			// Rounds without the clock reaching the poll interval: no new fetch jobs. (Retries of a
			// failing job follow the job's own backoff, bounded by max attempts.)
			for range 5 {
				e.clk.Advance(30 * time.Minute)
				e.drain(10)
			}
			after := countRequests(e, uri)
			require.LessOrEqual(t, after, 5, "work source re-fetched in a loop (%d requests)", after)
			e.clk.Advance(7 * time.Hour)
			e.drain(10)
			require.Greater(t, countRequests(e, uri), after, "not re-checked after the poll interval")
		})
	}
}

func restrictedRedirect(path string) fakeao3.Reply {
	return fakeao3.Reply{Status: 302, Header: map[string]string{"Location": "/users/login?restricted=true&return_to=" + strings.ReplaceAll(path, "/", "%2F")}}
}

// A tag whose page is restricted is marked attempted and waits for tags.info_ttl.
func TestRestrictedTagInfoDoesNotLoop(t *testing.T) {
	e := newEnv(t, 3, func(o *controllers.Options) { o.TagInfoTypes = []string{"Fandom"} })
	e.srv.Handle("/tags/Fixture%20Tag", restrictedRedirect("/tags/Fixture%20Tag"))
	e.add(e.site.TagPath())
	e.drain(50)
	for range 5 {
		e.clk.Advance(infoInterval + time.Minute) // past each info scheduling interval
		e.drain(50)
	}
	require.Equal(t, 1, countRequests(e, "/tags/Fixture%20Tag"), "restricted tag re-fetched")
	tag, err := e.db.TagByName(context.Background(), "Fixture Tag")
	require.NoError(t, err)
	require.False(t, tag.InfoFetchedAt.IsZero(), "attempt not recorded")
}

// The adult interstitial is a cookie problem, not the work's page: nothing is stored and the work
// stays unfetched, so a later fetch can succeed.
func TestAdultGateLeavesWorkUnfetched(t *testing.T) {
	e := newEnv(t, 3, nil)
	ctx := context.Background()
	e.srv.Handle("/works/5001", fakeao3.Reply{Body: fakeao3.AdultGate(5001)})
	e.add(e.site.TagPath())
	e.drain(50)
	require.Equal(t, 1, countRequests(e, "/works/5001?view_full_work=true"), "adult gate retried")
	_, _, err := e.db.LatestRawPage(ctx, 5001)
	require.ErrorIs(t, err, store.ErrNoRawPage, "interstitial stored as a raw page")
	require.True(t, e.diag(5001).LastFetched.IsZero(), "work marked fetched from an interstitial")
	jobs, err := e.db.FindJobs(ctx, model.JobFilter{State: model.JobFailed, DedupePrefix: "work:5001", Limit: 5})
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Contains(t, jobs[0].LastError, string(controllers.CodeAdultGate))

	// Once AO3 serves the page again, the work is fetched normally.
	e.srv.Handle("/works/5001", fakeao3.Reply{Body: fakeao3.WorkPage("<p>fixture body</p>")})
	e.queueWork(5001)
	e.drain(10)
	require.False(t, e.diag(5001).LastFetched.IsZero(), "work not fetched after the gate cleared")
}

// An oversized work page fails once, with no retries and no AO3 backoff, and is not queued again
// by later polls until the work changes.
func TestTooLargeWorkFailsOnce(t *testing.T) {
	e := newEnvWith(t, 3, nil, func(o *httpx.Options) { o.MaxBody = 128 << 10 })
	ctx := context.Background()
	e.srv.Handle("/works/5001", fakeao3.Reply{Body: fakeao3.WorkPage(strings.Repeat("x", 256<<10))})
	e.add(e.site.TagPath())
	e.drain(50)
	const uri = "/works/5001?view_full_work=true"
	require.Equal(t, 1, countRequests(e, uri), "oversized page retried")
	jobs, err := e.db.FindJobs(ctx, model.JobFilter{State: model.JobFailed, DedupePrefix: "work:5001", Limit: 5})
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, string(controllers.CodeTooLarge), jobs[0].LastError)
	require.Equal(t, 1, jobs[0].Attempts)
	require.Equal(t, 0, e.cl.PacerState().ConsecFails, "oversized page counted as an AO3 failure")
	for _, id := range []int64{5000, 5002} {
		require.NotZero(t, e.diag(id).ChapterRows, "work %d not mirrored", id)
	}
	require.Zero(t, e.diag(5001).ChapterRows, "oversized work has content")

	e.clk.Advance(7 * time.Hour)
	e.drain(50)
	require.Equal(t, 1, countRequests(e, uri), "oversized page re-queued by the next poll")
}

// Two strikes: a work that left the source (still on AO3) is fetched after each rescan that
// misses it and dropped from the source by the check after the second, with no further
// requests; a deleted one stays listed under it, also when it is deleted only after its first
// check found it.
func TestRescanDropsWorkThatLeftSource(t *testing.T) {
	e := newEnv(t, 5, func(o *controllers.Options) { o.Sources.FullRescanInterval = 24 * time.Hour })
	id := e.add(e.site.TagPath())
	e.drain(50)
	e.site.Update(5002, func(w *fakeao3.SiteWork) { w.Untagged = true })
	e.site.Update(5003, func(w *fakeao3.SiteWork) { w.Deleted = true })
	e.site.Update(5004, func(w *fakeao3.SiteWork) { w.Untagged = true })
	for i := range 3 { // three rescans
		e.clk.Advance(25 * time.Hour)
		e.drain(100)
		if i == 0 {
			e.site.Update(5004, func(w *fakeao3.SiteWork) { w.Deleted = true }) // after its first check
		}
	}
	require.Equal(t, 3, countRequests(e, "/works/5002?view_full_work=true"), "initial + a check after each of two misses")
	require.Zero(t, e.diag(5002).Sources, "work that left the source still a member")
	require.Equal(t, 1, e.diag(5003).Sources, "deleted work dropped from the source")
	require.Equal(t, 1, e.diag(5004).Sources, "work deleted after its first check dropped from the source")
	require.Equal(t, 4, e.source(id).MemberWorks)
}

// A rescan whose listing parse finds no work link in most blurbs (as after an AO3 markup
// change) counts no misses: nothing is checked or dropped, however many rescans run.
func TestRescanWithUnparsedBlurbsDropsNothing(t *testing.T) {
	e := newEnv(t, 5, func(o *controllers.Options) { o.Sources.FullRescanInterval = 24 * time.Hour })
	id := e.add(e.site.TagPath())
	e.drain(50)
	for _, w := range []int64{5001, 5002, 5003} {
		e.site.Update(w, func(sw *fakeao3.SiteWork) { sw.Unlinked = true })
	}
	for range 3 { // three rescans, each storing 2 blurbs and skipping 3
		e.clk.Advance(25 * time.Hour)
		e.drain(100)
	}
	for _, w := range []string{"5001", "5002", "5003"} {
		require.Equal(t, 1, countRequests(e, "/works/"+w+"?view_full_work=true"), "work %s checked after a rescan that skipped it", w)
	}
	require.Equal(t, 5, e.source(id).MemberWorks, "members dropped after rescans that skipped their blurbs")
	require.Contains(t, e.logs.String(), "rescan skipped more blurbs than it stored")
}

// A rescan whose listings lost their pagination nav (as after an AO3 markup change) stops
// after page 1. The heading's total shows the walk did not reach every work, so no misses are
// counted: nothing past page 1 is checked or dropped, however many rescans run.
func TestRescanWithoutPaginationDropsNothing(t *testing.T) {
	e := newEnv(t, 45, func(o *controllers.Options) { o.Sources.FullRescanInterval = 24 * time.Hour }) // 3 pages
	id := e.add(e.site.TagPath())
	e.drain(100)
	require.Equal(t, 45, e.source(id).MemberWorks)
	before := len(e.srv.Requests())
	e.site.SetNoPagination(true)
	for range 3 {
		e.clk.Advance(25 * time.Hour)
		e.drain(100)
	}
	for _, r := range e.srv.Requests()[before:] {
		require.False(t, strings.HasPrefix(r.URI, "/works/"), "work checked after a rescan that stopped early: %s", r.URI)
	}
	require.Equal(t, 45, e.source(id).MemberWorks, "members dropped after rescans that stopped at page 1")
	require.Contains(t, e.logs.String(), "listing total is more than the pages walked hold")
}

// A rescan that lists none of a source's works (here they all left; a blurb selector that no
// longer matches looks the same) counts no misses either.
func TestRescanListingNothingDropsNothing(t *testing.T) {
	e := newEnv(t, 3, func(o *controllers.Options) { o.Sources.FullRescanInterval = 24 * time.Hour })
	id := e.add(e.site.TagPath())
	e.drain(50)
	for _, w := range []int64{5000, 5001, 5002} {
		e.site.Update(w, func(sw *fakeao3.SiteWork) { sw.Untagged = true })
	}
	for range 3 {
		e.clk.Advance(25 * time.Hour)
		e.drain(100)
	}
	require.Equal(t, 3, e.source(id).MemberWorks, "members dropped after rescans that listed nothing")
	require.Equal(t, 1, countRequests(e, "/works/5000?view_full_work=true"), "work checked after a rescan that listed nothing")
	require.Contains(t, e.logs.String(), "rescan listed none of the source's works")
}

// A work that left the source and can't be fetched (here: over the body limit) gets one check
// after the first miss and is dropped at the second without a request, instead of being
// downloaded again after every rescan.
func TestRescanDropsUnfetchableWorkThatLeftSource(t *testing.T) {
	e := newEnvWith(t, 3, func(o *controllers.Options) { o.Sources.FullRescanInterval = 24 * time.Hour },
		func(o *httpx.Options) { o.MaxBody = 128 << 10 })
	e.srv.Handle("/works/5001", fakeao3.Reply{Body: fakeao3.WorkPage(strings.Repeat("x", 256<<10))})
	id := e.add(e.site.TagPath())
	e.drain(50)
	e.site.Update(5001, func(w *fakeao3.SiteWork) { w.Untagged = true })
	for range 3 { // three rescans
		e.clk.Advance(25 * time.Hour)
		e.drain(100)
	}
	require.Equal(t, 2, countRequests(e, "/works/5001?view_full_work=true"), "initial + one check after the first miss")
	require.Zero(t, e.diag(5001).Sources, "unfetchable work that left the source still a member")
	require.Equal(t, 2, e.source(id).MemberWorks)
}

// `source refresh --full` on a corrupt cursor restarts the walk, like the scheduler does; it
// must not mark a half-walked backfill as done.
func TestRefreshFullCorruptCursorRestartsWalk(t *testing.T) {
	e := newEnv(t, 45, nil)
	ctx := context.Background()
	id := e.add(e.site.TagPath())
	r := e.source(id)
	r.BackfillCursor = "{not json"
	require.NoError(t, e.db.UpdateSourceProgress(ctx, r))
	_, err := e.ctl.RefreshSource(ctx, id, true)
	require.NoError(t, err)
	cur, err := controllers.ParseSourceCursor(e.source(id).BackfillCursor)
	require.NoError(t, err)
	require.Equal(t, 1, cur.NextPage, "corrupt cursor reset to done")
	e.drain(200)
	require.Equal(t, 45, e.source(id).MemberWorks, "backfill did not walk the whole listing")
}

// A member work whose fetch keeps failing (here: the adult gate) is not re-queued by every poll;
// it waits a day before the stale-member check tries it again.
func TestFailingWorkNotRequeuedEveryPoll(t *testing.T) {
	e := newEnv(t, 3, nil)
	e.srv.Handle("/works/5001", fakeao3.Reply{Body: fakeao3.AdultGate(5001)})
	e.add(e.site.TagPath())
	e.drain(50)
	const uri = "/works/5001?view_full_work=true"
	require.Equal(t, 1, countRequests(e, uri))
	for range 3 { // three polls within a day
		e.clk.Advance(7 * time.Hour)
		e.drain(50)
	}
	require.Equal(t, 1, countRequests(e, uri), "failing work re-queued by every poll")
	e.clk.Advance(7 * time.Hour) // over a day since the failure
	e.drain(50)
	require.Equal(t, 2, countRequests(e, uri), "failing work never retried")
}

// A poll keeps walking past a page whose works are already mirrored (by another source or a
// direct fetch) but new to this source.
func TestPollContinuesForWorksNewToSource(t *testing.T) {
	e := newEnv(t, 3, nil)
	ctx := context.Background()
	id := e.add(e.site.TagPath())
	e.drain(50)
	// 25 new works (more than one page) appear, and all are mirrored directly, not via the source.
	newest := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	for i := range 25 {
		wid := int64(6000 + i)
		e.site.Add(fakeao3.SiteWork{ID: wid, Revised: newest.Add(-time.Duration(i) * time.Hour), Words: 500, Chapters: 1, Expected: 1})
		_, err := e.ctl.FetchWork(ctx, wid, 0, true)
		require.NoError(t, err)
	}
	e.clk.Advance(7 * time.Hour) // next poll
	e.drain(100)
	require.Equal(t, 28, e.source(id).MemberWorks, "poll stopped on page 1: works on page 2 not linked")
}

// A mirrored work that later grows past the body limit keeps its parser version, so `reparse`
// leaves it alone and doesn't roll its fetched revision back (which would queue the oversized
// download again on the next poll).
func TestTooLargeWorkNotReparsed(t *testing.T) {
	e := newEnvWith(t, 3, nil, func(o *httpx.Options) { o.MaxBody = 128 << 10 })
	ctx := context.Background()
	e.add(e.site.TagPath())
	e.drain(50)
	before := e.diag(5001).ParserVersion
	require.NotZero(t, before)
	e.srv.Handle("/works/5001", fakeao3.Reply{Body: fakeao3.WorkPage(strings.Repeat("x", 256<<10))})
	e.site.Update(5001, func(w *fakeao3.SiteWork) { w.Words += 100; w.Revised = time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC) })
	e.clk.Advance(7 * time.Hour)
	e.drain(50)
	const uri = "/works/5001?view_full_work=true"
	require.Equal(t, 2, countRequests(e, uri), "initial fetch + one oversized attempt")
	require.Equal(t, before, e.diag(5001).ParserVersion, "too-large work marked for reparse")

	res, err := e.ctl.Reparse(ctx, controllers.ReparseOptions{})
	require.NoError(t, err)
	require.Zero(t, res.Reparsed, "reparse picked up the too-large work")
	e.clk.Advance(7 * time.Hour)
	e.drain(50)
	require.Equal(t, 2, countRequests(e, uri), "oversized download queued again after reparse")
}

// A work fetched for a source that was removed meanwhile is still stored (the request was
// spent), just not linked to the gone source.
func TestFetchWorkForRemovedSourceKeepsPage(t *testing.T) {
	e := newEnv(t, 3, nil)
	ctx := context.Background()
	_, err := e.ctl.FetchWork(ctx, 5001, 999, true) // source 999 does not exist (removed mid-fetch)
	require.NoError(t, err)
	_, _, err = e.db.LatestRawPage(ctx, 5001)
	require.NoError(t, err, "page rolled back because the source was gone")
	require.False(t, e.diag(5001).LastFetched.IsZero())
	require.Zero(t, e.diag(5001).Sources)
}

// Tag and series info fetches that fail before any page (too large) or keep failing (AO3
// unavailable through every retry) are marked attempted, not re-queued every minute.
func TestInfoFetchFailuresDoNotLoop(t *testing.T) {
	e := newEnvWith(t, 0, func(o *controllers.Options) { o.SeriesInfo = true; o.TagInfoTypes = []string{"Fandom"} },
		func(o *httpx.Options) { o.MaxBody = 128 << 10 })
	e.srv.Handle("/works/1001", fakeao3.Reply{Body: fixtureBody(t, "work_chaptered.html")}) // part 2 of series 3001
	e.srv.Handle("/series/3001", fakeao3.Reply{Body: fakeao3.Page(strings.Repeat("x", 256<<10))})
	e.queueWork(1001)
	e.drain(5)
	for range 40 {
		e.clk.Advance(infoInterval + time.Minute) // past each info scheduling interval
		e.drain(20)
	}
	require.Equal(t, 1, countRequests(e, "/series/3001"), "oversized series page re-fetched")

	// A tag that is unavailable on every attempt is given up after its last retry.
	e2 := newEnv(t, 3, func(o *controllers.Options) { o.TagInfoTypes = []string{"Fandom"} })
	e2.srv.Handle("/tags/Fixture%20Tag", fakeao3.Reply{Status: 525, Body: fakeao3.Cloudflare525})
	e2.add(e2.site.TagPath())
	e2.drain(50)
	for range 100 {
		e2.clk.Advance(5 * time.Minute) // past each retry backoff
		e2.drain(20)
	}
	n := countRequests(e2, "/tags/Fixture%20Tag")
	require.LessOrEqual(t, n, 5, "unavailable tag re-queued after its last attempt (%d requests)", n)
	tag, err := e2.db.TagByName(context.Background(), "Fixture Tag")
	require.NoError(t, err)
	require.False(t, tag.InfoFetchedAt.IsZero(), "attempt not recorded after the last retry")
}

// While AO3's robots.txt is unreadable, jobs wait for it to be re-read without using up their
// attempts, then run normally.
func TestRobotsOutageWaitsWithoutAttempts(t *testing.T) {
	e := newEnv(t, 3, nil)
	ctx := context.Background()
	e.add(e.site.TagPath())
	e.drain(50)
	e.site.Update(5001, func(w *fakeao3.SiteWork) { w.Words += 10 })
	// robots.txt is rate limited for its next three reads, then fine.
	busy := fakeao3.Reply{Status: 429, Header: map[string]string{"Retry-After": "60"}}
	e.srv.SetRobots(busy, busy, busy, fakeao3.Reply{Body: fakeao3.Robots})
	e.clk.Advance(25 * time.Hour) // the cached robots.txt expires
	e.queueWork(5001)
	before := countRequests(e, "/works/5001?view_full_work=true")
	for range 3 {
		e.drain(20)
		jobs, err := e.db.FindJobs(ctx, model.JobFilter{DedupeKey: "work:5001", State: model.JobQueued, Limit: 1})
		require.NoError(t, err)
		require.Len(t, jobs, 1, "job not waiting during the robots outage")
		require.Zero(t, jobs[0].Attempts, "robots outage used up an attempt")
		e.clk.Advance(16 * time.Minute) // past the robots error TTL
	}
	e.drain(20)
	require.Equal(t, before+1, countRequests(e, "/works/5001?view_full_work=true"), "work not fetched after robots recovered")
}

// An odd AO3 answer for a work (an unusual 4xx) is not a parse failure: nothing is stored and
// the work is not marked fetched.
func TestOddWorkResponseNotStored(t *testing.T) {
	e := newEnv(t, 3, nil)
	ctx := context.Background()
	e.srv.Handle("/works/5001", fakeao3.Reply{Status: 410, Body: fakeao3.Page("<p>gone-ish</p>")})
	e.add(e.site.TagPath())
	e.drain(50)
	_, _, err := e.db.LatestRawPage(ctx, 5001)
	require.ErrorIs(t, err, store.ErrNoRawPage, "error page stored as a raw page")
	require.True(t, e.diag(5001).LastFetched.IsZero(), "work marked fetched from an error page")
	jobs, err := e.db.FindJobs(ctx, model.JobFilter{DedupeKey: "work:5001", Limit: 5})
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Contains(t, jobs[0].LastError, string(controllers.CodeOddResponse))
}

// An odd answer for a backfill page holds that page back for a poll interval; it neither
// blocks the backfill until a parser change nor re-queues the page every round.
func TestOddListingResponseHoldsBackfill(t *testing.T) {
	e := newEnv(t, 45, nil)
	id := e.add(e.site.TagPath()) // page 1 parsed fine; backfill page 2 gets the odd answer
	e.srv.Handle(e.site.TagPath(), fakeao3.Reply{Status: 410, Body: fakeao3.Page("<p>odd</p>")})
	e.drain(200)
	listing := func() int {
		n := 0
		for _, r := range e.srv.Requests() {
			if strings.HasPrefix(r.URI, e.site.TagPath()) {
				n++
			}
		}
		return n
	}
	after := listing()
	cur, err := controllers.ParseSourceCursor(e.source(id).BackfillCursor)
	require.NoError(t, err)
	require.Zero(t, cur.BlockedParser, "odd response blocked the backfill as a parse failure")
	require.NotZero(t, cur.RetryAt, "page not held back")
	e.clk.Advance(30 * time.Minute)
	e.drain(50)
	require.Equal(t, after, listing(), "held-back page re-fetched before its retry time")

	// Once AO3 answers normally the backfill continues.
	e.srv.Handle(e.site.TagPath()) // no replies: the synthetic site serves it again
	e.clk.Advance(7 * time.Hour)
	e.drain(500)
	require.Equal(t, model.SourceDoneBackfill, e.source(id).State, "backfill did not resume")
}
