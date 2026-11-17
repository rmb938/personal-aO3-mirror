package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/fakeao3"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/sources"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

const testUA = "personal-ao3-mirror/test (personal offline archive; contact: discord @fixture_user)"

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	c.Advance(d)
	return ctx.Err()
}
func (c *fakeClock) Advance(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

type env struct {
	t    *testing.T
	srv  *fakeao3.Server
	site *fakeao3.Site
	clk  *fakeClock
	st   *store.Store
	cl   *httpx.Client
	srcs *sources.Service
	sch  *Scheduler
	logs *bytes.Buffer
}

func newEnv(t *testing.T, works int, opts func(*Options)) *env {
	t.Helper()
	e := &env{t: t, srv: fakeao3.New(), clk: &fakeClock{now: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)}, logs: &bytes.Buffer{}}
	t.Cleanup(e.srv.Close)
	e.site = fakeao3.NewSite("Fixture Tag", 5000, works)
	e.srv.SetSite(e.site)
	log := slog.New(slog.NewJSONHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	var err error
	e.cl, err = httpx.New(httpx.Options{BaseURL: e.srv.URL, UserAgent: testUA, Interval: 10 * time.Second, Clock: e.clk, Rand: rand.NewPCG(7, 7), Logger: log})
	require.NoError(t, err)
	dataDir := t.TempDir()
	e.st, err = store.Open(context.Background(), dataDir, store.Options{Now: e.clk.Now, Logger: log})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, e.st.Close()) })
	cfg := config.Default().Sources
	e.srcs = sources.New(sources.Options{Store: e.st, Client: e.cl, Config: cfg, Interval: 10 * time.Second, Now: e.clk.Now, Logger: log})
	o := Options{Store: e.st, Client: e.cl, Now: e.clk.Now, DataDir: dataDir, Logger: log}
	if opts != nil {
		opts(&o)
	}
	e.sch = New(o)
	return e
}

// drain runs steps until nothing is runnable (bounded).
func (e *env) drain(limit int) int {
	e.t.Helper()
	for n := range limit {
		ran, err := e.sch.Step(context.Background())
		require.NoError(e.t, err)
		if !ran {
			return n
		}
	}
	require.FailNow(e.t, "scheduler still running", "after %d steps", limit)
	return limit
}

func (e *env) add(url string, o sources.AddOptions) int64 {
	e.t.Helper()
	res, err := e.srcs.Add(context.Background(), url, o)
	require.NoError(e.t, err)
	return res.Source.ID
}

func (e *env) source(id int64) store.SourceRow {
	e.t.Helper()
	src, err := e.st.GetSource(context.Background(), id)
	require.NoError(e.t, err)
	return src
}

func (e *env) enqueue(j store.Job) {
	e.t.Helper()
	_, _, err := e.st.EnqueueJob(context.Background(), j)
	require.NoError(e.t, err)
}

// scan runs a single-row query against meta.
func (e *env) scan(q string, dst ...any) {
	e.t.Helper()
	require.NoError(e.t, e.st.Meta.QueryRow(q).Scan(dst...))
}

func (e *env) fetchedCount() int {
	var n int
	e.scan(`SELECT COUNT(*) FROM works WHERE last_fetched > 0`, &n)
	return n
}

func (e *env) workRequests() map[string]int {
	out := map[string]int{}
	for _, r := range e.srv.Requests() {
		if strings.HasPrefix(r.URI, "/works/") {
			out[r.URI]++
		}
	}
	return out
}

func fixtureBody(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", name))
	require.NoError(t, err)
	return string(b)
}

func TestPollRetriesUnfetchedDeepWork(t *testing.T) {
	e := newEnv(t, 45, nil)
	ctx := context.Background()
	// 5000 is the oldest work: page 3 of the newest-first update order. Its fetch keeps failing
	// until its attempts are exhausted.
	e.srv.Handle("/works/5000", fakeao3.Reply{Status: 525, Body: fakeao3.Cloudflare525})
	e.add(e.site.TagPath(), sources.AddOptions{})
	for range 10 {
		e.drain(200)
		e.clk.Advance(3 * time.Hour)
	}
	failed, err := e.st.ListJobs(ctx, store.JobFailed, 0, "work:5000", 5)
	require.NoError(t, err)
	require.NotEmpty(t, failed, "expected failed jobs for 5000")
	e.srv.Handle("/works/5000") // AO3 recovered
	// A plain poll (pages 1-2 unchanged) must still reach page 3 and re-queue the unfetched work.
	e.clk.Advance(7 * time.Hour)
	e.drain(200)
	var fetched int64
	e.scan(`SELECT last_fetched FROM works WHERE id = 5000`, &fetched)
	require.NotZero(t, fetched, "poll should re-queue a work whose earlier fetches failed, not wait for the rescan")
}

func TestHandlerPanicFailsJob(t *testing.T) {
	e := newEnv(t, 1, nil)
	ctx := context.Background()
	e.sch.handlers[KindWork] = func(context.Context, store.Job, *slog.Logger) (map[string]any, error) {
		panic("fixture panic value")
	}
	e.enqueue(store.Job{Kind: KindWork, Target: "5000", Priority: ClassOnDemand, DedupeKey: "work:5000"})
	e.drain(5) // must not panic out of Step
	js, err := e.st.ListJobs(ctx, store.JobFailed, 0, "work:5000", 1)
	require.NoError(t, err)
	require.Len(t, js, 1)
	require.Equal(t, "panic", js[0].LastError, "panicking job should fail with code panic")
	require.NotContains(t, e.logs.String(), "fixture panic value", "panic value leaked into logs")
}

func TestJobFinishedDuringShutdownIsDone(t *testing.T) {
	e := newEnv(t, 1, nil)
	ctx, cancel := context.WithCancel(context.Background())
	e.sch.handlers[KindWork] = func(context.Context, store.Job, *slog.Logger) (map[string]any, error) {
		cancel() // shutdown arrives just as the job succeeds
		return nil, nil
	}
	e.enqueue(store.Job{Kind: KindWork, Target: "5000", Priority: ClassOnDemand, DedupeKey: "work:5000"})
	_, err := e.sch.Step(ctx)
	require.NoError(t, err)
	js, err := e.st.ListJobs(context.Background(), "", 0, "work:5000", 1)
	require.NoError(t, err)
	require.Len(t, js, 1)
	require.Equal(t, store.JobDone, js[0].State, "job that succeeded during shutdown should be done")
}

func TestPollPageCapWarns(t *testing.T) {
	e := newEnv(t, 45, func(o *Options) { o.MaxPollPages = 1 })
	id := e.add(e.site.TagPath(), sources.AddOptions{})
	e.drain(500)
	require.NotContains(t, e.logs.String(), "poll page cap reached", "backfill must not trigger the poll cap warning")
	// Page 1 of the update order changes, and more pages exist: the 1-page cap cuts the poll short.
	e.site.Add(fakeao3.SiteWork{ID: 9999, Revised: time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC), Kudos: 1, Hits: 1, Words: 10, Chapters: 1, Expected: 1})
	e.clk.Advance(7 * time.Hour)
	e.drain(100)
	logs := e.logs.String()
	require.Contains(t, logs, "poll page cap reached")
	require.Contains(t, logs, `"max_poll_pages":1`)
	require.Contains(t, logs, fmt.Sprintf(`"source_id":%d`, id))
}

func TestBackfillThenPoll(t *testing.T) {
	e := newEnv(t, 45, nil)
	ctx := context.Background()
	id := e.add(e.site.TagPath(), sources.AddOptions{})
	e.drain(500)

	src := e.source(id)
	require.Equal(t, store.SourceDoneBackfill, src.State)
	require.Equal(t, 45, src.MemberWorks)
	require.Equal(t, 45, src.FetchedWorks)
	require.Equal(t, 45, e.fetchedCount())
	for uri, n := range e.workRequests() {
		require.Equal(t, 1, n, "work %s request count", uri)
		require.True(t, strings.HasSuffix(uri, "?view_full_work=true"), "work %s fetched without view_full_work", uri)
	}
	chs, err := e.st.ChapterBodies(ctx, 5000)
	require.NoError(t, err)
	require.Len(t, chs, 1)
	require.Contains(t, chs[0].BodyHTML, "Fixture body 5000")
	listingPages := 0
	for _, r := range e.srv.Requests() {
		if strings.HasPrefix(r.URI, "/tags/") {
			listingPages++
		}
	}
	require.Equal(t, 3, listingPages, "45 works = 3 pages, page 1 at add time")

	// Update: one work gains a chapter (newest revised), one new work appears.
	e.site.Update(5010, func(w *fakeao3.SiteWork) {
		w.Chapters, w.Expected, w.Words, w.Revised = w.Chapters+1, 0, w.Words+500, time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	})
	e.site.Add(fakeao3.SiteWork{ID: 9999, Revised: time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC), Kudos: 1, Hits: 1, Words: 10, Chapters: 1, Expected: 1})
	before := len(e.srv.Requests())
	e.clk.Advance(7 * time.Hour) // past poll interval
	e.drain(100)
	var newReqs []string
	for _, r := range e.srv.Requests()[before:] {
		newReqs = append(newReqs, r.URI)
	}
	joined := strings.Join(newReqs, " ")
	require.Contains(t, joined, "/works/5010?", "poll should fetch the changed work")
	require.Contains(t, joined, "/works/9999?", "poll should fetch the new work")
	works := 0
	for _, u := range newReqs {
		if strings.HasPrefix(u, "/works/") {
			works++
		}
	}
	require.Equal(t, 2, works, "poll requests: %v", newReqs)
	require.LessOrEqual(t, len(newReqs), 4, "poll page 1 (+ maybe page 2 stop), 2 works: %v", newReqs)
	require.Contains(t, newReqs[0], "sort_column%5D=revised_at", "poll must sort by revised")
	var ch int
	e.scan(`SELECT chapters_posted FROM works WHERE id = 5010`, &ch)
	require.GreaterOrEqual(t, ch, 2, "updated work not refreshed")

	// Content-free change details: why the listing triggered the fetch, and what the page changed.
	logs := e.logs.String()
	require.Contains(t, logs, `"msg":"work needs fetch"`)
	require.Contains(t, logs, `"work_id":5010,"page":1,"never_fetched":false,"revised_listed":"2026-11-14"`)
	require.Contains(t, logs, `"meta_changed":[`, "re-fetch of 5010 should report meta_changed")
}

func TestFairnessAlternates(t *testing.T) {
	e := newEnv(t, 60, nil)
	ctx := context.Background()
	// Two sources over the same synthetic tag with different filters (distinct sources).
	a := e.add(e.site.TagPath()+"?work_search%5Blanguage_id%5D=en", sources.AddOptions{})
	b := e.add(e.site.TagPath()+"?work_search%5Bcomplete%5D=T", sources.AddOptions{})
	var order []int64
	for range 12 {
		e.clk.Advance(time.Second)
		ran, err := e.sch.Step(ctx)
		require.NoError(t, err)
		require.True(t, ran)
		var src int64
		e.scan(`SELECT COALESCE(source_id,0) FROM jobs WHERE state != 'queued' ORDER BY started_at DESC, id DESC LIMIT 1`, &src)
		order = append(order, src)
	}
	seenA, seenB := 0, 0
	for _, s := range order {
		switch s {
		case a:
			seenA++
		case b:
			seenB++
		}
	}
	require.GreaterOrEqual(t, seenA, 4, "sources not interleaved: %v", order)
	require.GreaterOrEqual(t, seenB, 4, "sources not interleaved: %v", order)
}

func TestPauseCancelAndRecover(t *testing.T) {
	e := newEnv(t, 45, nil)
	ctx := context.Background()
	id := e.add(e.site.TagPath(), sources.AddOptions{})
	_, err := e.sch.Step(ctx) // backfill page 2 (class 4) or work jobs
	require.NoError(t, err)
	require.NoError(t, e.srcs.Pause(ctx, id))
	before := len(e.srv.Requests())
	require.Equal(t, 0, e.drain(10), "paused source ran jobs")
	require.Len(t, e.srv.Requests(), before, "paused source made requests")
	require.NoError(t, e.srcs.Resume(ctx, id))
	// Simulate a crash mid-job: claim a job and leave it running.
	j, err := e.st.PickJob(ctx, []string{KindWork, KindBackfill}, e.clk.Now())
	require.NoError(t, err)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	require.NoError(t, e.sch.Run(cctx)) // recovers running jobs, then stops immediately
	got, err := e.st.GetJob(ctx, j.ID)
	require.NoError(t, err)
	require.Equal(t, store.JobQueued, got.State, "crashed job not recovered")
	n, err := e.srcs.Cancel(ctx, id)
	require.NoError(t, err)
	require.NotZero(t, n, "cancel should drop queued jobs")
	require.Equal(t, 0, e.drain(10), "cancelled source ran jobs")
}

func TestUnavailableRetriesWithBackoff(t *testing.T) {
	e := newEnv(t, 5, nil)
	ctx := context.Background()
	e.add(e.site.TagPath(), sources.AddOptions{})
	e.srv.Handle("/works/5000", fakeao3.Reply{Status: 525, Body: fakeao3.Cloudflare525})
	e.drain(20)
	jobs, err := e.st.ListJobs(ctx, "", 0, "work:5000", 5)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, store.JobQueued, jobs[0].State)
	require.True(t, jobs[0].NotBefore.After(e.clk.Now()), "retry must wait for the backoff")
	require.Equal(t, "unavailable", jobs[0].LastError)
	require.Equal(t, 4, e.fetchedCount(), "other works still progressed")
}

func TestDeletedWorkKeepsContent(t *testing.T) {
	e := newEnv(t, 3, nil)
	ctx := context.Background()
	e.add(e.site.TagPath(), sources.AddOptions{})
	e.drain(50)
	e.site.Update(5001, func(w *fakeao3.SiteWork) { w.Deleted = true })
	e.enqueue(store.Job{Kind: KindWork, Target: "5001", Priority: ClassOnDemand, DedupeKey: "work:5001"})
	e.drain(10)
	st, _, err := e.st.WorkStatus(ctx, 5001)
	require.NoError(t, err)
	require.Equal(t, model.StatusDeleted, st)
	chs, err := e.st.ChapterBodies(ctx, 5001)
	require.NoError(t, err)
	require.NotEmpty(t, chs, "deleted work keeps its content")
}

func TestWorkSource(t *testing.T) {
	e := newEnv(t, 3, nil)
	id := e.add("/works/5002/chapters/1", sources.AddOptions{})
	e.drain(10)
	require.Equal(t, 1, e.source(id).FetchedWorks)
	require.Len(t, e.workRequests(), 1)
	// Not re-fetched before the poll interval, re-fetched after.
	e.drain(5)
	require.Equal(t, 1, e.workRequests()["/works/5002?view_full_work=true"], "refetched too early")
	e.clk.Advance(7 * time.Hour)
	e.drain(5)
	require.Equal(t, 2, e.workRequests()["/works/5002?view_full_work=true"], "not refreshed after poll interval")
}

func TestWindowedBackfill(t *testing.T) {
	// 90 works over ~1.7 years, result window 40 => backfill must slice by date windows.
	e := newEnv(t, 90, func(o *Options) { o.MaxResults = 40; o.WindowDays = 120 })
	id := e.add(e.site.TagPath(), sources.AddOptions{})
	e.drain(2000)
	src := e.source(id)
	require.Equal(t, 90, src.MemberWorks)
	require.Equal(t, 90, src.FetchedWorks)
	require.Equal(t, store.SourceDoneBackfill, src.State)
	windowed := 0
	for _, r := range e.srv.Requests() {
		if strings.Contains(r.URI, "date_from") {
			windowed++
		}
	}
	require.NotZero(t, windowed, "expected date-window requests")
}

func TestLogsContentFree(t *testing.T) {
	e := newEnv(t, 3, nil)
	e.add(e.site.TagPath(), sources.AddOptions{})
	e.drain(50)
	low := strings.ToLower(e.logs.String())
	for _, word := range []string{"fixture work", "fixture body", "fixture summary", "fixture_author", "fixture tag"} {
		require.NotContains(t, low, word, "logs leaked content")
	}
	require.Contains(t, e.logs.String(), `"msg":"job done"`, "expected job logs")
	evs, err := e.st.JobEvents(context.Background(), 1)
	require.NoError(t, err)
	require.NotEmpty(t, evs, "expected job events")
	for _, ev := range evs {
		require.NotContains(t, strings.ToLower(ev.Detail), "fixture", "event leaked content")
	}
}

func TestTagInfoFetched(t *testing.T) {
	e := newEnv(t, 3, func(o *Options) { o.TagInfoTypes = []string{"Fandom"} })
	e.srv.Handle("/tags/Fixture%20Tag", fakeao3.Reply{Body: fixtureBody(t, "tag_synonym.html")})
	e.add(e.site.TagPath(), sources.AddOptions{})
	e.drain(50)
	e.clk.Advance(2 * time.Minute) // tag scheduling is throttled to once a minute
	e.drain(50)
	var fetchedAt int64
	var merger sql.NullInt64
	e.scan(`SELECT info_fetched_at, merger_id FROM tags WHERE name = 'Fixture Tag'`, &fetchedAt, &merger)
	require.NotZero(t, fetchedAt, "tag info not stored")
	require.True(t, merger.Valid, "tag merger not stored")
	tagReqs := 0
	for _, r := range e.srv.Requests() {
		if r.URI == "/tags/Fixture%20Tag" {
			tagReqs++
		}
		// Rating tags are not in info_types: never fetched.
		require.False(t, strings.HasPrefix(r.URI, "/tags/General"), "unexpected tag fetch %s", r.URI)
	}
	require.Equal(t, 1, tagReqs, "tag page request count")
}

func TestRescanVerifiesMissingWorks(t *testing.T) {
	e := newEnv(t, 5, func(o *Options) { o.RescanInterval = 24 * time.Hour })
	ctx := context.Background()
	id := e.add(e.site.TagPath(), sources.AddOptions{})
	e.drain(50)
	src := e.source(id)
	require.Equal(t, store.SourceDoneBackfill, src.State)
	require.False(t, src.LastFullScanAt.IsZero())
	e.site.Update(5003, func(w *fakeao3.SiteWork) { w.Deleted = true })
	e.site.Update(5001, func(w *fakeao3.SiteWork) { w.Kudos = 9999 }) // stats-only change
	before := len(e.srv.Requests())
	e.clk.Advance(25 * time.Hour)
	e.drain(100)
	st, _, err := e.st.WorkStatus(ctx, 5003)
	require.NoError(t, err)
	require.Equal(t, model.StatusDeleted, st, "missing work verified as deleted")
	var kudos int
	e.scan(`SELECT kudos FROM works WHERE id = 5001`, &kudos)
	require.Equal(t, 9999, kudos, "rescan refreshes stats")
	var works []string
	for _, r := range e.srv.Requests()[before:] {
		if strings.HasPrefix(r.URI, "/works/") {
			works = append(works, r.URI)
		}
	}
	require.Equal(t, []string{"/works/5003?view_full_work=true"}, works, "rescan should only fetch the missing work")
	src = e.source(id)
	cur, err := store.ParseCursor(src.BackfillCursor)
	require.NoError(t, err)
	require.Equal(t, store.SourceDoneBackfill, src.State)
	require.False(t, cur.Rescan)
	require.True(t, src.LastFullScanAt.After(e.clk.Now().Add(-time.Hour)), "last full scan updated")
}

func TestWorkFetchQueuesImages(t *testing.T) {
	e := newEnv(t, 0, func(o *Options) { o.QueueImages = true })
	e.srv.Handle("/works/1001", fakeao3.Reply{Body: fixtureBody(t, "work_chaptered.html")})
	e.enqueue(store.Job{Kind: KindWork, Target: "1001", Priority: ClassOnDemand, DedupeKey: "work:1001"})
	e.drain(5)
	counts, _, err := e.st.ImageCounts(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, counts["queued"], "expected the fixture's one embedded image queued")
}

func TestSeriesSourceStoresMetadata(t *testing.T) {
	e := newEnv(t, 0, nil)
	e.srv.Handle("/series/3001", fakeao3.Reply{Body: fixtureBody(t, "series.html")})
	e.add("/series/3001", sources.AddOptions{})
	var desc string
	var count, words int
	e.scan(`SELECT summary_html, works_count, words FROM series WHERE id = 3001`, &desc, &count, &words)
	require.Contains(t, desc, "series description", "series metadata from add")
	require.Equal(t, 2, count)
	require.Equal(t, 13145, words)
	// Poll refreshes it.
	_, err := e.st.Meta.Exec(`UPDATE series SET words = 0 WHERE id = 3001`)
	require.NoError(t, err)
	e.clk.Advance(7 * time.Hour)
	e.drain(20)
	e.scan(`SELECT words FROM series WHERE id = 3001`, &words)
	require.Equal(t, 13145, words, "poll refreshes series words")
}

func TestSeriesInfoForNonSourceSeries(t *testing.T) {
	e := newEnv(t, 0, func(o *Options) { o.SeriesInfo = true })
	ctx := context.Background()
	e.srv.Handle("/works/1001", fakeao3.Reply{Body: fixtureBody(t, "work_chaptered.html")}) // part 2 of series 3001
	e.srv.Handle("/series/3001", fakeao3.Reply{Body: fixtureBody(t, "series.html")})
	e.enqueue(store.Job{Kind: KindWork, Target: "1001", Priority: ClassOnDemand, DedupeKey: "work:1001"})
	e.drain(5)
	e.clk.Advance(2 * time.Minute)
	e.drain(5)
	var desc string
	var fetched int64
	e.scan(`SELECT summary_html, last_fetched FROM series WHERE id = 3001`, &desc, &fetched)
	require.Contains(t, desc, "series description", "series info stored")
	require.NotZero(t, fetched)
	ok, err := e.st.WorkExists(ctx, 999)
	require.NoError(t, err)
	require.False(t, ok, "series info must not add works we do not mirror")
	// Not refetched again until stale.
	e.clk.Advance(2 * time.Minute)
	e.drain(5)
	n := 0
	for _, r := range e.srv.Requests() {
		if r.URI == "/series/3001" {
			n++
		}
	}
	require.Equal(t, 1, n, "series page fetch count")
}
