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
		e.clk.Advance(2 * time.Minute) // tag scheduling runs at most once a minute
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
	jobs, err := e.db.FindJobs(ctx, store.JobFilter{State: store.JobFailed, DedupePrefix: "work:5001", Limit: 5})
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
	jobs, err := e.db.FindJobs(ctx, store.JobFilter{State: store.JobFailed, DedupePrefix: "work:5001", Limit: 5})
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
