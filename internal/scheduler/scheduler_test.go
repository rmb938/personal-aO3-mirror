package scheduler

import (
	"bytes"
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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
	e.cl, err = httpx.New(httpx.Options{BaseURL: e.srv.URL, UserAgent: testUA, Interval: 10 * time.Second, Clock: e.clk, Seed: 7, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	e.st, err = store.Open(context.Background(), dataDir, store.Options{Now: e.clk.Now, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.st.Close() })
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
func (e *env) drain(max int) int {
	e.t.Helper()
	n := 0
	for ; n < max; n++ {
		ran, err := e.sch.Step(context.Background())
		if err != nil {
			e.t.Fatal(err)
		}
		if !ran {
			return n
		}
	}
	e.t.Fatalf("still running after %d steps", max)
	return n
}

func (e *env) add(url string, o sources.AddOptions) int64 {
	e.t.Helper()
	res, err := e.srcs.Add(context.Background(), url, o)
	if err != nil {
		e.t.Fatal(err)
	}
	return res.Source.ID
}

func (e *env) fetchedCount() int {
	var n int
	e.st.Meta.QueryRow(`SELECT COUNT(*) FROM works WHERE last_fetched > 0`).Scan(&n)
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

func TestBackfillThenPoll(t *testing.T) {
	e := newEnv(t, 45, nil)
	ctx := context.Background()
	id := e.add(e.site.TagPath(), sources.AddOptions{})
	e.drain(500)

	src, _ := e.st.GetSource(ctx, id)
	if src.State != store.SourceDoneBackfill || src.MemberWorks != 45 || src.FetchedWorks != 45 || e.fetchedCount() != 45 {
		t.Fatalf("after backfill: state=%s members=%d fetched=%d", src.State, src.MemberWorks, src.FetchedWorks)
	}
	for uri, n := range e.workRequests() {
		if n != 1 || !strings.HasSuffix(uri, "?view_full_work=true") {
			t.Fatalf("work %s requested %d times", uri, n)
		}
	}
	if chs, _ := e.st.ChapterBodies(ctx, 5000); len(chs) != 1 || !strings.Contains(chs[0].BodyHTML, "Fixture body 5000") {
		t.Fatalf("content: %+v", chs)
	}
	listingPages := 0
	for _, r := range e.srv.Requests() {
		if strings.HasPrefix(r.URI, "/tags/") {
			listingPages++
		}
	}
	if listingPages != 3 { // 45 works = 3 pages, page 1 at add time
		t.Fatalf("listing requests = %d", listingPages)
	}

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
	if !strings.Contains(joined, "/works/5010?") || !strings.Contains(joined, "/works/9999?") {
		t.Fatalf("poll should fetch the changed and new works: %v", newReqs)
	}
	works := 0
	for _, u := range newReqs {
		if strings.HasPrefix(u, "/works/") {
			works++
		}
	}
	if works != 2 || len(newReqs) > 4 { // poll page 1 (+ maybe page 2 stop), 2 works
		t.Fatalf("poll made too many requests: %v", newReqs)
	}
	if !strings.Contains(newReqs[0], "sort_column%5D=revised_at") {
		t.Fatalf("poll must sort by revised: %s", newReqs[0])
	}
	var ch int
	e.st.Meta.QueryRow(`SELECT chapters_posted FROM works WHERE id = 5010`).Scan(&ch)
	if ch < 2 {
		t.Fatalf("updated work not refreshed: chapters=%d", ch)
	}
}

func TestFairnessAlternates(t *testing.T) {
	e := newEnv(t, 60, nil)
	ctx := context.Background()
	// Two sources over the same synthetic tag with different filters (distinct sources).
	a := e.add(e.site.TagPath()+"?work_search%5Blanguage_id%5D=en", sources.AddOptions{})
	b := e.add(e.site.TagPath()+"?work_search%5Bcomplete%5D=T", sources.AddOptions{})
	var order []int64
	for i := 0; i < 12; i++ {
		e.clk.Advance(time.Second)
		ran, err := e.sch.Step(ctx)
		if err != nil || !ran {
			t.Fatal(err, ran)
		}
		var src int64
		e.st.Meta.QueryRow(`SELECT COALESCE(source_id,0) FROM jobs WHERE state != 'queued' ORDER BY started_at DESC, id DESC LIMIT 1`).Scan(&src)
		order = append(order, src)
	}
	seenA, seenB := 0, 0
	for _, s := range order {
		if s == a {
			seenA++
		}
		if s == b {
			seenB++
		}
	}
	if seenA < 4 || seenB < 4 {
		t.Fatalf("sources not interleaved: %v (a=%d b=%d)", order, a, b)
	}
}

func TestPauseCancelAndRecover(t *testing.T) {
	e := newEnv(t, 45, nil)
	ctx := context.Background()
	id := e.add(e.site.TagPath(), sources.AddOptions{})
	e.sch.Step(ctx) // backfill page 2 (class 4) or work jobs
	if err := e.srcs.Pause(ctx, id); err != nil {
		t.Fatal(err)
	}
	before := len(e.srv.Requests())
	if n := e.drain(10); n != 0 {
		t.Fatalf("paused source ran %d jobs", n)
	}
	if len(e.srv.Requests()) != before {
		t.Fatal("paused source made requests")
	}
	e.srcs.Resume(ctx, id)
	// Simulate a crash mid-job: claim a job and leave it running.
	j, err := e.st.PickJob(ctx, []string{KindWork, KindBackfill}, e.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_ = e.sch.Run(cctx) // recovers running jobs, then stops immediately
	got, _ := e.st.GetJob(ctx, j.ID)
	if got.State != store.JobQueued {
		t.Fatalf("crashed job not recovered: %s", got.State)
	}
	n, _ := e.srcs.Cancel(ctx, id)
	if n == 0 {
		t.Fatal("cancel should drop queued jobs")
	}
	if ran := e.drain(10); ran != 0 {
		t.Fatalf("cancelled source ran %d jobs", ran)
	}
}

func TestUnavailableRetriesWithBackoff(t *testing.T) {
	e := newEnv(t, 5, nil)
	ctx := context.Background()
	e.add(e.site.TagPath(), sources.AddOptions{})
	e.srv.Handle("/works/5000", fakeao3.Reply{Status: 525, Body: fakeao3.Cloudflare525}, fakeao3.Reply{Status: 200, Body: ""})
	e.srv.Handle("/works/5000", fakeao3.Reply{Status: 525, Body: fakeao3.Cloudflare525})
	e.drain(20)
	jobs, _ := e.st.ListJobs(ctx, "", 0, "work:5000", 5)
	if len(jobs) != 1 || jobs[0].State != store.JobQueued || !jobs[0].NotBefore.After(e.clk.Now()) || jobs[0].LastError != "unavailable" {
		t.Fatalf("job after 525: %+v", jobs)
	}
	// Other works still progressed.
	if e.fetchedCount() != 4 {
		t.Fatalf("fetched %d", e.fetchedCount())
	}
}

func TestDeletedWorkKeepsContent(t *testing.T) {
	e := newEnv(t, 3, nil)
	ctx := context.Background()
	e.add(e.site.TagPath(), sources.AddOptions{})
	e.drain(50)
	e.site.Update(5001, func(w *fakeao3.SiteWork) { w.Deleted = true })
	e.st.EnqueueJob(ctx, store.Job{Kind: KindWork, Target: "5001", Priority: ClassOnDemand, DedupeKey: "work:5001"})
	e.drain(10)
	st, _, _ := e.st.WorkStatus(ctx, 5001)
	chs, _ := e.st.ChapterBodies(ctx, 5001)
	if st != model.StatusDeleted || len(chs) == 0 {
		t.Fatalf("status=%s chapters=%d", st, len(chs))
	}
}

func TestWorkSource(t *testing.T) {
	e := newEnv(t, 3, nil)
	ctx := context.Background()
	id := e.add("/works/5002/chapters/1", sources.AddOptions{})
	e.drain(10)
	src, _ := e.st.GetSource(ctx, id)
	if src.FetchedWorks != 1 || len(e.workRequests()) != 1 {
		t.Fatalf("work source: %+v %v", src, e.workRequests())
	}
	// Not re-fetched before the poll interval, re-fetched after.
	e.drain(5)
	if e.workRequests()["/works/5002?view_full_work=true"] != 1 {
		t.Fatal("refetched too early")
	}
	e.clk.Advance(7 * time.Hour)
	e.drain(5)
	if e.workRequests()["/works/5002?view_full_work=true"] != 2 {
		t.Fatal("not refreshed after poll interval")
	}
}

func TestWindowedBackfill(t *testing.T) {
	// 90 works over ~1.7 years, result window 40 => backfill must slice by date windows.
	e := newEnv(t, 90, func(o *Options) { o.MaxResults = 40; o.WindowDays = 120 })
	ctx := context.Background()
	id := e.add(e.site.TagPath(), sources.AddOptions{})
	e.drain(2000)
	src, _ := e.st.GetSource(ctx, id)
	if src.MemberWorks != 90 || src.FetchedWorks != 90 || src.State != store.SourceDoneBackfill {
		t.Fatalf("windowed: members=%d fetched=%d state=%s", src.MemberWorks, src.FetchedWorks, src.State)
	}
	windowed := 0
	for _, r := range e.srv.Requests() {
		if strings.Contains(r.URI, "date_from") {
			windowed++
		}
	}
	if windowed == 0 {
		t.Fatal("expected date-window requests")
	}
}

func TestLogsContentFree(t *testing.T) {
	e := newEnv(t, 3, nil)
	e.add(e.site.TagPath(), sources.AddOptions{})
	e.drain(50)
	low := strings.ToLower(e.logs.String())
	for _, word := range []string{"fixture work", "fixture body", "fixture summary", "fixture_author", "fixture tag"} {
		if strings.Contains(low, word) {
			t.Fatalf("logs leaked %q", word)
		}
	}
	if !strings.Contains(e.logs.String(), `"msg":"job done"`) {
		t.Fatal("expected job logs")
	}
	evs, _ := e.st.JobEvents(context.Background(), 1)
	if len(evs) == 0 {
		t.Fatal("expected job events")
	}
	for _, ev := range evs {
		if strings.Contains(strings.ToLower(ev.Detail), "fixture") {
			t.Fatalf("event leaked content: %s", ev.Detail)
		}
	}
	_ = strconv.Itoa
}
