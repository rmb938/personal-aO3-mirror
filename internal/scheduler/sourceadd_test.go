package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/fakeao3"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// A queued add runs in the scheduler like any job: page 1 goes through the one paced client.
func TestQueuedSourceAdd(t *testing.T) {
	e := newEnv(t, 3, nil)
	ctx := context.Background()
	q, err := e.ctl.QueueSourceAdd(ctx, e.site.TagPath(), controllers.AddSourceOptions{})
	require.NoError(t, err)
	require.True(t, q.Created)
	require.Empty(t, e.srv.Requests(), "queueing an add made a request")
	e.drain(50)
	o, err := e.ctl.SourceAddOutcome(ctx, q.JobID)
	require.NoError(t, err)
	require.Equal(t, model.JobDone, o.State)
	require.NotNil(t, o.Result)
	require.Equal(t, 3, o.Result.Estimate.Total)
	require.Equal(t, 3, e.source(o.Result.Source.ID).FetchedWorks, "source not backfilled")
	_, err = e.db.GetSourceAdd(ctx, 1)
	require.ErrorIs(t, err, store.ErrNoSourceAdd, "queued add kept after success")

	_, err = e.ctl.QueueSourceAdd(ctx, e.site.TagPath(), controllers.AddSourceOptions{})
	require.ErrorAs(t, err, &controllers.ExistsError{})
}

func TestQueuedSourceAddFailures(t *testing.T) {
	e := newEnv(t, 3, nil)
	ctx := context.Background()

	// Not found: fails once and drops the queued add.
	e.srv.Handle("/tags/Missing/works", fakeao3.Reply{Status: 404, Body: fakeao3.Page("Error 404")})
	q, err := e.ctl.QueueSourceAdd(ctx, "/tags/Missing/works", controllers.AddSourceOptions{})
	require.NoError(t, err)
	e.drain(10)
	o, err := e.ctl.SourceAddOutcome(ctx, q.JobID)
	require.NoError(t, err)
	require.Equal(t, model.JobFailed, o.State)
	require.Equal(t, string(controllers.CodeNotFound), o.Error)
	_, err = e.db.GetSourceAdd(ctx, 1)
	require.ErrorIs(t, err, store.ErrNoSourceAdd)

	// Unavailable: retried after the backoff, then dropped once attempts run out.
	e.srv.Handle("/tags/Busy/works", fakeao3.Reply{Status: 525, Body: fakeao3.Cloudflare525})
	q, err = e.ctl.QueueSourceAdd(ctx, "/tags/Busy/works", controllers.AddSourceOptions{})
	require.NoError(t, err)
	for range 10 {
		e.drain(10)
		e.clk.Advance(3 * time.Hour) // past every backoff and the breaker
	}
	o, err = e.ctl.SourceAddOutcome(ctx, q.JobID)
	require.NoError(t, err)
	require.Equal(t, model.JobFailed, o.State)
	_, err = e.db.GetSourceAdd(ctx, 2)
	require.ErrorIs(t, err, store.ErrNoSourceAdd, "queued add left behind after its last attempt")
	busy := 0
	for _, r := range e.srv.Requests() {
		if strings.HasPrefix(r.URI, "/tags/Busy/works") {
			busy++
		}
	}
	require.Equal(t, 5, busy, "attempts")
}

// A robots.txt outage on the add job's last attempt keeps the queued add: nothing was sent, and
// the scheduler requeues the job.
func TestQueuedSourceAddKeptOnRobotsOutage(t *testing.T) {
	e := newEnv(t, 3, nil)
	ctx := context.Background()
	e.srv.SetRobots(fakeao3.Reply{Status: 429}, fakeao3.Reply{Body: fakeao3.Robots})
	_, err := e.ctl.QueueSourceAdd(ctx, e.site.TagPath(), controllers.AddSourceOptions{})
	require.NoError(t, err)
	_, err = e.ctl.AddQueuedSource(ctx, 1, true) // as if on its last attempt
	require.ErrorAs(t, err, &controllers.WaitError{})
	_, err = e.db.GetSourceAdd(ctx, 1)
	require.NoError(t, err, "queued add dropped during a robots outage")
}

// An odd AO3 answer on page 1 is reported as such, not as a parse failure.
func TestQueuedSourceAddOddResponse(t *testing.T) {
	e := newEnv(t, 3, nil)
	ctx := context.Background()
	e.srv.Handle("/tags/Odd/works", fakeao3.Reply{Status: 410, Body: fakeao3.Page("<p>odd</p>")})
	q, err := e.ctl.QueueSourceAdd(ctx, "/tags/Odd/works", controllers.AddSourceOptions{})
	require.NoError(t, err)
	e.drain(10)
	o, err := e.ctl.SourceAddOutcome(ctx, q.JobID)
	require.NoError(t, err)
	require.Equal(t, model.JobFailed, o.State)
	require.Contains(t, o.Error, string(controllers.CodeOddResponse))
	require.NotContains(t, o.Error, string(controllers.CodeParseUnexpected))
}

// The add estimate decides windowing with the same result limit as the backfill.
func TestAddEstimateUsesMaxResults(t *testing.T) {
	e := newEnv(t, 90, func(o *controllers.Options) { o.MaxResults = 40 })
	res, err := e.ctl.AddSource(context.Background(), e.site.TagPath(), controllers.AddSourceOptions{Confirm: true})
	require.NoError(t, err)
	require.True(t, res.Estimate.Windowed, "estimate disagrees with the backfill about windowing")
	require.Equal(t, 90, res.Estimate.Total)
}
