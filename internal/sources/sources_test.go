package sources

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/fakeao3"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
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
	srv *fakeao3.Server
	st  *store.Store
	svc *Service
}

func fixture(t *testing.T, name string) string {
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func newEnv(t *testing.T, autoMax int) *env {
	t.Helper()
	srv := fakeao3.New()
	t.Cleanup(srv.Close)
	clk := &fakeClock{now: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)}
	c, err := httpx.New(httpx.Options{BaseURL: srv.URL, UserAgent: testUA, Interval: 10 * time.Second, Clock: clk, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), t.TempDir(), store.Options{Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := config.Default().Sources
	cfg.AutoConfirmMax = autoMax
	return &env{srv: srv, st: st, svc: New(Options{Store: st, Client: c, Config: cfg, Interval: 10 * time.Second, Now: clk.Now})}
}

const tagURL = "/tags/Fixture%20Fandom/works"

func TestAddTagSourceGate(t *testing.T) {
	for _, c := range []struct {
		autoMax int
		confirm bool
		want    store.SourceState
	}{
		{5000, false, store.SourceActive},
		{100, false, store.SourcePendingConfirm},
		{100, true, store.SourceActive},
	} {
		e := newEnv(t, c.autoMax)
		e.srv.Handle(tagURL, fakeao3.Reply{Body: fixture(t, "listing_tag.html")})
		res, err := e.svc.Add(context.Background(), "https://archiveofourown.org"+tagURL+"?work_search%5Blanguage_id%5D=en", AddOptions{Confirm: c.confirm})
		if err != nil {
			t.Fatal(err)
		}
		if res.Source.State != c.want {
			t.Errorf("autoMax=%d confirm=%v: state %s, want %s", c.autoMax, c.confirm, res.Source.State, c.want)
		}
		est := res.Estimate
		// fixture: 1,234 works => 62 pages; page 1 done; 2 blurbs both new
		if est.Total != 1234 || est.Pages != 62 || est.ListingRequests != 61 || est.WorkFetches != 1234 ||
			est.Requests != 61+1234 || est.Duration != time.Duration(61+1234)*10*time.Second || est.Windowed {
			t.Errorf("estimate: %+v", est)
		}
		got, _ := e.st.GetSource(context.Background(), res.Source.ID)
		if got.MemberWorks != 2 || got.BackfillCursor != `{"next_page":2,"order":"kudos_desc"}` ||
			got.FiltersJSON != `{"work_search[language_id]":["en"]}` {
			t.Errorf("stored source: %+v", got)
		}
		// The one request (after robots) used the order and filters, page 1.
		reqs := e.srv.Requests()
		last := reqs[len(reqs)-1].URI
		if !strings.Contains(last, "sort_column%5D=kudos_count") || !strings.Contains(last, "language_id%5D=en") || strings.Contains(last, "page=") {
			t.Errorf("request %s", last)
		}
	}
}

func TestAddDuplicateAndErrors(t *testing.T) {
	e := newEnv(t, 5000)
	ctx := context.Background()
	e.srv.Handle(tagURL, fakeao3.Reply{Body: fixture(t, "listing_tag.html")})
	res, err := e.svc.Add(ctx, tagURL, AddOptions{})
	if err != nil {
		t.Fatal(err)
	}
	before := len(e.srv.Requests())
	var ex ExistsError
	if _, err := e.svc.Add(ctx, "/tags/Fixture%20Fandom", AddOptions{}); !errors.As(err, &ex) || ex.ID != res.Source.ID {
		t.Fatalf("duplicate: %v", err)
	}
	if len(e.srv.Requests()) != before {
		t.Fatal("duplicate add must not make requests")
	}

	e.srv.Handle("/tags/Gone/works", fakeao3.Reply{Status: 404, Body: fakeao3.Page("Error 404")})
	if _, err := e.svc.Add(ctx, "/tags/Gone/works", AddOptions{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("404: %v", err)
	}
	e.srv.Handle("/tags/Busy/works", fakeao3.Reply{Status: 525, Body: fakeao3.Cloudflare525})
	if _, err := e.svc.Add(ctx, "/tags/Busy/works", AddOptions{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("525: %v", err)
	}
	if _, err := e.svc.Add(ctx, "/works/search?x=1", AddOptions{}); err == nil {
		t.Fatal("search URL must be rejected")
	}
	if _, err := e.svc.Add(ctx, tagURL+"x", AddOptions{Order: "bogus"}); err == nil {
		t.Fatal("bad order must be rejected")
	}
	list, _ := e.st.ListSources(ctx)
	if len(list) != 1 {
		t.Fatalf("failed adds must not create sources: %d", len(list))
	}
}

func TestAddSeriesAndWork(t *testing.T) {
	e := newEnv(t, 5000)
	ctx := context.Background()
	e.srv.Handle("/series/3001", fakeao3.Reply{Body: fixture(t, "series.html")})
	res, err := e.svc.Add(ctx, "/series/3001", AddOptions{Order: "hits_desc"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Estimate.Total != 2 || res.Blurbs != 2 || res.Source.State != store.SourceActive || res.Source.BackfillOrder != "hits_desc" {
		t.Fatalf("series: %+v", res)
	}
	reqs := e.srv.Requests()
	if last := reqs[len(reqs)-1].URI; last != "/series/3001" {
		t.Fatalf("series request must have no sort params: %s", last)
	}
	n := len(e.srv.Requests())
	w, err := e.svc.Add(ctx, "/works/1001/chapters/2002", AddOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if w.Source.Kind != "work" || w.Source.Target != "1001" || w.Estimate.Requests != 1 || len(e.srv.Requests()) != n {
		t.Fatalf("work source: %+v (requests %d->%d)", w, n, len(e.srv.Requests()))
	}
}

func TestStateTransitions(t *testing.T) {
	e := newEnv(t, 1)
	ctx := context.Background()
	e.srv.Handle(tagURL, fakeao3.Reply{Body: fixture(t, "listing_tag.html")})
	res, err := e.svc.Add(ctx, tagURL, AddOptions{})
	if err != nil {
		t.Fatal(err)
	}
	id := res.Source.ID
	if res.Source.State != store.SourcePendingConfirm {
		t.Fatalf("state %s", res.Source.State)
	}
	if err := e.svc.Resume(ctx, id); err == nil {
		t.Fatal("resume of pending must fail")
	}
	steps := []func() error{
		func() error { return e.svc.Confirm(ctx, id) },
		func() error { return e.svc.Pause(ctx, id) },
		func() error { return e.svc.Resume(ctx, id) },
		func() error { _, err := e.svc.Cancel(ctx, id); return err },
	}
	for i, f := range steps {
		if err := f(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	if _, err := e.svc.Cancel(ctx, id); err == nil {
		t.Fatal("double cancel must fail")
	}
	if err := e.svc.Remove(ctx, id); err != nil {
		t.Fatal(err)
	}
}
