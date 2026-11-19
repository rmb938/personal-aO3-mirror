package controllers

import (
	"context"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/fakeao3"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/jobs"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

const testUA = "personal-ao3-mirror/test (personal offline archive; contact: discord @fixture_user)"

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
	return ctx.Err()
}

type env struct {
	srv  *fakeao3.Server
	st   *store.Store
	svc  *Controllers
	opts Options // what svc was built with (tests derive variants, e.g. a wrapped Client)
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", name))
	require.NoError(t, err)
	return string(b)
}

func newEnv(t *testing.T, autoMax int) *env {
	t.Helper()
	srv := fakeao3.New()
	t.Cleanup(srv.Close)
	clk := &fakeClock{now: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)}
	c, err := httpx.New(httpx.Options{BaseURL: srv.URL, UserAgent: testUA, Interval: 10 * time.Second, Clock: clk, Rand: rand.NewPCG(1, 1)})
	require.NoError(t, err)
	st, err := store.Open(context.Background(), t.TempDir(), store.Options{Now: clk.Now})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	cfg := config.Default().Sources
	cfg.AutoConfirmMax = autoMax
	o := Options{Store: st, Client: c, Sources: cfg, Interval: 10 * time.Second, Now: clk.Now}
	return &env{srv: srv, st: st, svc: New(o), opts: o}
}

const tagURL = "/tags/Fixture%20Fandom/works"

func TestAddTagSourceGate(t *testing.T) {
	for _, c := range []struct {
		autoMax int
		confirm bool
		want    model.SourceState
	}{
		{5000, false, model.SourceActive},
		{100, false, model.SourcePendingConfirm},
		{100, true, model.SourceActive},
	} {
		e := newEnv(t, c.autoMax)
		e.srv.Handle(tagURL, fakeao3.Reply{Body: fixture(t, "listing_tag.html")})
		res, err := e.svc.AddSource(context.Background(), "https://archiveofourown.org"+tagURL+"?work_search%5Blanguage_id%5D=en", AddSourceOptions{Confirm: c.confirm})
		require.NoError(t, err)
		assert.Equal(t, c.want, res.Source.State, "autoMax=%d confirm=%v", c.autoMax, c.confirm)
		// fixture: 1,234 works => 62 pages; page 1 done; 2 blurbs both new
		assert.Equal(t, Estimate{Total: 1234, Pages: 62, ListingRequests: 61, WorkFetches: 1234, Requests: 61 + 1234,
			Duration: time.Duration(61+1234) * 10 * time.Second, DiskBytes: res.Estimate.DiskBytes}, res.Estimate)
		got, err := e.svc.GetSource(context.Background(), res.Source.ID)
		require.NoError(t, err)
		assert.Equal(t, 2, got.MemberWorks)
		assert.JSONEq(t, `{"next_page":2,"order":"kudos_desc"}`, got.BackfillCursor)
		assert.JSONEq(t, `{"work_search[language_id]":["en"]}`, got.FiltersJSON)
		// The one request (after robots) used the order and filters, page 1.
		reqs := e.srv.Requests()
		last := reqs[len(reqs)-1].URI
		assert.Contains(t, last, "sort_column%5D=kudos_count")
		assert.Contains(t, last, "language_id%5D=en")
		assert.NotContains(t, last, "page=")
	}
}

func TestAddDuplicateAndErrors(t *testing.T) {
	e := newEnv(t, 5000)
	ctx := context.Background()
	e.srv.Handle(tagURL, fakeao3.Reply{Body: fixture(t, "listing_tag.html")})
	res, err := e.svc.AddSource(ctx, tagURL, AddSourceOptions{})
	require.NoError(t, err)
	before := len(e.srv.Requests())
	_, err = e.svc.AddSource(ctx, "/tags/Fixture%20Fandom", AddSourceOptions{})
	var ex ExistsError
	require.ErrorAs(t, err, &ex, "duplicate")
	require.Equal(t, res.Source.ID, ex.ID)
	require.Len(t, e.srv.Requests(), before, "duplicate add must not make requests")

	e.srv.Handle("/tags/Gone/works", fakeao3.Reply{Status: 404, Body: fakeao3.Page("Error 404")})
	_, err = e.svc.AddSource(ctx, "/tags/Gone/works", AddSourceOptions{})
	require.ErrorIs(t, err, ErrNotFound)
	e.srv.Handle("/tags/Busy/works", fakeao3.Reply{Status: 525, Body: fakeao3.Cloudflare525})
	_, err = e.svc.AddSource(ctx, "/tags/Busy/works", AddSourceOptions{})
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = e.svc.AddSource(ctx, "/works/search?x=1", AddSourceOptions{})
	require.Error(t, err, "search URL must be rejected")
	_, err = e.svc.AddSource(ctx, tagURL+"x", AddSourceOptions{Order: "bogus"})
	require.Error(t, err, "bad order must be rejected")
	list, total, err := e.svc.ListSources(ctx, 0, 10)
	require.NoError(t, err)
	require.Len(t, list, 1, "failed adds must not create sources")
	require.Equal(t, 1, total)
}

func TestAddSeriesAndWork(t *testing.T) {
	e := newEnv(t, 5000)
	ctx := context.Background()
	e.srv.Handle("/series/3001", fakeao3.Reply{Body: fixture(t, "series.html")})
	res, err := e.svc.AddSource(ctx, "/series/3001", AddSourceOptions{Order: "hits_desc"})
	require.NoError(t, err)
	require.Equal(t, 2, res.Estimate.Total)
	require.Equal(t, 2, res.Blurbs)
	require.Equal(t, model.SourceActive, res.Source.State)
	require.Equal(t, "hits_desc", res.Source.BackfillOrder)
	reqs := e.srv.Requests()
	require.Equal(t, "/series/3001", reqs[len(reqs)-1].URI, "series request must have no sort params")
	n := len(e.srv.Requests())
	w, err := e.svc.AddSource(ctx, "/works/1001/chapters/2002", AddSourceOptions{})
	require.NoError(t, err)
	require.Equal(t, "work", w.Source.Kind)
	require.Equal(t, "1001", w.Source.Target)
	require.Equal(t, 1, w.Estimate.Requests)
	require.Len(t, e.srv.Requests(), n, "adding a work source makes no requests")
}

func TestStateTransitions(t *testing.T) {
	e := newEnv(t, 1)
	ctx := context.Background()
	e.srv.Handle(tagURL, fakeao3.Reply{Body: fixture(t, "listing_tag.html")})
	res, err := e.svc.AddSource(ctx, tagURL, AddSourceOptions{})
	require.NoError(t, err)
	id := res.Source.ID
	require.Equal(t, model.SourcePendingConfirm, res.Source.State)
	require.Error(t, e.svc.ResumeSource(ctx, id), "resume of pending must fail")
	require.NoError(t, e.svc.ConfirmSource(ctx, id))
	require.NoError(t, e.svc.PauseSource(ctx, id))
	require.NoError(t, e.svc.ResumeSource(ctx, id))
	_, err = e.svc.CancelSource(ctx, id)
	require.NoError(t, err)
	_, err = e.svc.CancelSource(ctx, id)
	require.Error(t, err, "double cancel must fail")
	require.NoError(t, e.svc.RemoveSource(ctx, id))
}

// SourceAddOutcome only reports on source add jobs: any other job, or no job, is ErrNoJob.
func TestSourceAddOutcomeOnlyForSourceAdds(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	var other int64
	require.NoError(t, e.st.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		var err error
		other, _, err = tx.EnqueueJob(ctx, store.NewJob{Kind: jobs.KindWork, Target: "1", DedupeKey: jobs.WorkKey(1)})
		return err
	}))
	_, err := e.svc.SourceAddOutcome(ctx, other)
	require.ErrorIs(t, err, store.ErrNoJob)
	_, err = e.svc.SourceAddOutcome(ctx, other+100)
	require.ErrorIs(t, err, store.ErrNoJob)
}

// ListJobs returns a page and the total of all matching jobs.
func TestListJobsPageAndTotal(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	var ids []int64
	require.NoError(t, e.st.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		for i := range int64(3) {
			id, _, err := tx.EnqueueJob(ctx, store.NewJob{Kind: jobs.KindWork, Target: "1", DedupeKey: jobs.WorkKey(i + 1)})
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		_, _, err := tx.EnqueueJob(ctx, store.NewJob{Kind: jobs.KindTagInfo, Target: "9", DedupeKey: jobs.TagKey(9)})
		return err
	}))
	page, total, err := e.svc.ListJobs(ctx, store.JobFilter{Kind: jobs.KindWork, Offset: 1, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Len(t, page, 1)
	require.Equal(t, ids[1], page[0].ID, "second newest work job")
	_, _, err = e.svc.ListJobs(ctx, store.JobFilter{Limit: 0})
	require.Error(t, err)
}
