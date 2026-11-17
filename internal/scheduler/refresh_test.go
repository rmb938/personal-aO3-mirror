package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/fakeao3"
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
	id := e.add(e.site.TagPath())
	// Let 5002 exhaust its attempts (backoff between them is skipped by advancing the clock).
	for range 10 {
		e.drain(50)
		e.clk.Advance(3 * time.Hour)
	}
	// Polls every 6h also re-queue it (still unfetched), so there may be several failed jobs.
	failed, err := e.db.ListJobs(ctx, store.JobFailed, 0, "work:5002", 5)
	require.NoError(t, err)
	require.NotEmpty(t, failed, "expected failed jobs for 5002")
	e.srv.Handle("/works/5002") // AO3 recovered: the synthetic site serves it again

	before := listingRequests(e)
	res, err := e.ctl.RefreshSource(ctx, id, false)
	require.NoError(t, err)
	require.Equal(t, 1, res.RequeuedWorks)
	require.True(t, res.PollNow)
	e.drain(50)
	require.NotEqual(t, before, listingRequests(e), "refresh should poll right away")
	st, _, err := e.db.WorkStatus(ctx, 5002)
	require.NoError(t, err)
	require.Equal(t, "ok", string(st))
	require.False(t, e.diag(5002).LastFetched.IsZero(), "re-queued work was not fetched")
	// The old failed jobs are now resolved by the later success: refreshing again must not
	// re-fetch the work.
	res, err = e.ctl.RefreshSource(ctx, id, false)
	require.NoError(t, err)
	require.Equal(t, 0, res.RequeuedWorks, "second refresh re-queued already-fetched works")
}

func TestRefreshFullAndSet(t *testing.T) {
	e := newEnv(t, 5, nil)
	ctx := context.Background()
	id := e.add(e.site.TagPath())
	e.drain(50)
	require.Equal(t, store.SourceDoneBackfill, e.source(id).State)
	res, err := e.ctl.RefreshSource(ctx, id, true)
	require.NoError(t, err)
	require.True(t, res.RescanStarted)
	require.Equal(t, store.SourceActive, e.source(id).State, "full refresh should reactivate")
	e.drain(50)
	src := e.source(id)
	cur, err := controllers.ParseCursor(src.BackfillCursor)
	require.NoError(t, err)
	require.Equal(t, store.SourceDoneBackfill, src.State, "after full refresh")
	require.False(t, cur.Rescan, "after full refresh")

	// set: shorter poll interval moves the next poll earlier; priority changes.
	prio := 7
	require.NoError(t, e.ctl.SetSourceSettings(ctx, id, 30*time.Minute, &prio))
	src = e.source(id)
	require.Equal(t, 30*time.Minute, src.PollInterval)
	require.Equal(t, 7, src.Priority)
	require.False(t, src.NextPollAt.After(e.clk.Now().Add(31*time.Minute)), "next poll %s", src.NextPollAt)
	require.Error(t, e.ctl.SetSourceSettings(ctx, id, time.Minute, nil), "poll interval below 10m must be rejected")
	require.NoError(t, e.ctl.PauseSource(ctx, id))
	_, err = e.ctl.RefreshSource(ctx, id, false)
	require.Error(t, err, "refresh of a paused source must fail")
}
