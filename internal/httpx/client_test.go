package httpx

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/fakeao3"
)

const testUA = "personal-ao3-mirror/test (personal offline archive; contact: discord @fixture_user)"

// fakeClock advances instantly on Sleep and records every sleep.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return ctx.Err()
}

func (c *fakeClock) total() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	var t time.Duration
	for _, s := range c.sleeps {
		t += s
	}
	return t
}

type harness struct {
	srv   *fakeao3.Server
	clock *fakeClock
	c     *Client
	logs  *bytes.Buffer
	recs  []RequestRecord
}

func newHarness(t *testing.T, interval time.Duration, jitter float64) *harness {
	t.Helper()
	h := &harness{srv: fakeao3.New(), clock: newFakeClock(), logs: &bytes.Buffer{}}
	t.Cleanup(h.srv.Close)
	logger := slog.New(slog.NewJSONHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c, err := New(Options{
		BaseURL: h.srv.URL, UserAgent: testUA, Interval: interval, Jitter: jitter,
		Logger: logger, Clock: h.clock, Seed: 42,
		OnRequest: func(r RequestRecord) { h.recs = append(h.recs, r) },
	})
	if err != nil {
		t.Fatal(err)
	}
	h.c = c
	return h
}

func (h *harness) get(t *testing.T, kind Kind, ref string) *Response {
	t.Helper()
	r, err := h.c.Get(context.Background(), kind, ref)
	if err != nil {
		t.Fatalf("Get %s: %v", ref, err)
	}
	return r
}

func TestUserAgentOnEveryRequest(t *testing.T) {
	h := newHarness(t, 10*time.Second, 0.2)
	h.srv.Handle("/works/1", fakeao3.Reply{Status: 302, Header: map[string]string{"Location": "/works/2"}})
	h.srv.Handle("/works/2", fakeao3.Reply{Body: fakeao3.WorkPage("fixture body")})
	h.srv.Handle("/tags/fixture/works", fakeao3.Reply{Body: fakeao3.Page("")})
	h.get(t, KindWork, "/works/1")
	h.get(t, KindListing, "/tags/fixture/works?page=2")
	reqs := h.srv.Requests()
	if len(reqs) != 4 { // robots + 2 hops + listing
		t.Fatalf("want 4 requests, got %d: %+v", len(reqs), reqs)
	}
	for _, r := range reqs {
		if r.UserAgent != testUA {
			t.Errorf("%s sent UA %q", r.URI, r.UserAgent)
		}
		if got := r.Header.Get("Sec-Fetch-Site"); got != "none" {
			t.Errorf("%s sent Sec-Fetch-Site %q, want none", r.URI, got)
		}
		// Only the truthful Sec-Fetch-Site: nothing that claims a browser tab or a human click.
		for _, k := range []string{"Sec-Fetch-User", "Sec-Fetch-Mode", "Sec-Fetch-Dest", "Upgrade-Insecure-Requests"} {
			if v := r.Header.Get(k); v != "" {
				t.Errorf("%s sent %s: %q", r.URI, k, v)
			}
		}
	}
	if reqs[0].URI != "/robots.txt" {
		t.Errorf("first request must be robots.txt, got %s", reqs[0].URI)
	}
}

func TestViewAdultCookieSent(t *testing.T) {
	h := newHarness(t, 10*time.Second, 0)
	h.srv.Handle("/works/7", fakeao3.Reply{Body: fakeao3.WorkPage("x")})
	h.get(t, KindWork, "/works/7?view_full_work=true")
	for _, r := range h.srv.Requests() {
		if !strings.Contains(r.Cookie, "view_adult=true") {
			t.Errorf("%s: missing view_adult cookie (Cookie=%q)", r.URI, r.Cookie)
		}
	}
}

func TestNewRequiresUA(t *testing.T) {
	for _, ua := range []string{"", "   ", "some-bot/1.0"} {
		if _, err := New(Options{UserAgent: ua}); !errors.Is(err, ErrNoUA) {
			t.Errorf("UA %q: want ErrNoUA, got %v", ua, err)
		}
	}
}

func TestRobotsDisallowedNeverSent(t *testing.T) {
	h := newHarness(t, 10*time.Second, 0)
	for _, ref := range []string{
		"/downloads/1/Fixture.html",
		"/works?page=2",
		"/works/search?work_search%5Bquery%5D=x",
		"/tags/search?x=1",
		"/autocomplete/tag?term=x",
	} {
		_, err := h.c.Get(context.Background(), KindPage, ref)
		if !errors.Is(err, ErrDisallowed) {
			t.Errorf("%s: want ErrDisallowed, got %v", ref, err)
		}
	}
	for _, r := range h.srv.Requests() {
		if r.URI != "/robots.txt" {
			t.Errorf("disallowed URL was sent: %s", r.URI)
		}
	}
	// Allowed forms we rely on.
	h.srv.Handle("/works/5", fakeao3.Reply{Body: fakeao3.WorkPage("x")})
	h.srv.Handle("/tags/fixture/works", fakeao3.Reply{Body: fakeao3.Page("")})
	for _, ref := range []string{
		"/works/5?view_full_work=true",
		"/tags/fixture/works?work_search%5Bsort_column%5D=revised_at&page=3",
	} {
		if _, err := h.c.Get(context.Background(), KindPage, ref); err != nil {
			t.Errorf("%s: want allowed, got %v", ref, err)
		}
	}
}

func TestForeignHostRefused(t *testing.T) {
	h := newHarness(t, 10*time.Second, 0)
	if _, err := h.c.Get(context.Background(), KindPage, "https://example.com/x"); !errors.Is(err, ErrForeign) {
		t.Fatalf("want ErrForeign, got %v", err)
	}
	if n := len(h.srv.Requests()); n != 0 {
		t.Fatalf("want 0 requests, got %d", n)
	}
}

func TestPacingFloorAndInterval(t *testing.T) {
	h := newHarness(t, config.MinInterval, 0.5) // max jitter at the floor: must never go below 3s
	h.srv.Handle("/works/1", fakeao3.Reply{Body: fakeao3.WorkPage("x")})
	for i := 0; i < 20; i++ {
		h.get(t, KindWork, "/works/1")
	}
	var prev time.Time
	for i, r := range h.recs {
		if i > 0 && r.Time.Sub(prev) < config.MinInterval {
			t.Fatalf("request %d only %s after previous (floor %s)", i, r.Time.Sub(prev), config.MinInterval)
		}
		prev = r.Time
	}

	h2 := newHarness(t, 10*time.Second, 0.2)
	h2.srv.Handle("/works/1", fakeao3.Reply{Body: fakeao3.WorkPage("x")})
	for i := 0; i < 20; i++ {
		h2.get(t, KindWork, "/works/1")
	}
	for i := 1; i < len(h2.recs); i++ {
		gap := h2.recs[i].Time.Sub(h2.recs[i-1].Time)
		if gap < 8*time.Second || gap > 12*time.Second+time.Second {
			t.Fatalf("gap %d = %s outside 10s +/-20%%", i, gap)
		}
	}
}

func TestRateLimitedHonorsRetryAfterAndSlowsDown(t *testing.T) {
	h := newHarness(t, 10*time.Second, 0)
	h.srv.Handle("/works/1",
		fakeao3.Reply{Status: 429, Header: map[string]string{"Retry-After": "300"}, Body: "slow down"},
		fakeao3.Reply{Body: fakeao3.WorkPage("x")})
	r := h.get(t, KindWork, "/works/1")
	if r.Class != ClassRateLimited {
		t.Fatalf("class = %s", r.Class)
	}
	before := h.clock.total()
	r = h.get(t, KindWork, "/works/1")
	if r.Class != ClassOK {
		t.Fatalf("class = %s", r.Class)
	}
	if waited := h.clock.total() - before; waited < 300*time.Second {
		t.Fatalf("waited %s, want >= Retry-After 300s", waited)
	}
	if st := h.c.PacerState(); st.Factor != 1.5 || st.Interval != 15*time.Second {
		t.Fatalf("want slowdown 1.5x => 15s, got %+v", st)
	}
}

func TestUnavailableBackoffAndBreaker(t *testing.T) {
	h := newHarness(t, 10*time.Second, 0)
	h.srv.Handle("/works/1", fakeao3.Reply{Status: 525, Body: fakeao3.Cloudflare525})
	var waits []time.Duration
	for i := 0; i < 5; i++ {
		before := h.clock.total()
		r := h.get(t, KindWork, "/works/1")
		if r.Class != ClassUnavailable || !strings.Contains(r.Reason, "status=525") {
			t.Fatalf("attempt %d: %s %s", i, r.Class, r.Reason)
		}
		waits = append(waits, h.clock.total()-before)
	}
	// waits[0] includes robots + first request spacing; from attempt 2 on: 1m, 2m, 4m, 8m.
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute}
	for i, w := range want {
		if waits[i+1] != w {
			t.Errorf("backoff %d = %s, want %s", i+1, waits[i+1], w)
		}
	}
	st := h.c.PacerState()
	if !st.BreakerOpen || st.ConsecFails != 5 {
		t.Fatalf("breaker should be open after 5 failures: %+v", st)
	}
	h.srv.Handle("/works/1", fakeao3.Reply{Body: fakeao3.WorkPage("x")})
	before := h.clock.total()
	h.get(t, KindWork, "/works/1")
	if w := h.clock.total() - before; w < 2*time.Hour {
		t.Fatalf("breaker should hold 2h, waited %s", w)
	}
	if st := h.c.PacerState(); st.ConsecFails != 0 {
		t.Fatalf("success should reset failures: %+v", st)
	}
}

func TestClassification(t *testing.T) {
	h := newHarness(t, 10*time.Second, 0)
	cases := []struct {
		path  string
		reply fakeao3.Reply
		kind  Kind
		want  Class
	}{
		{"/works/10", fakeao3.Reply{Body: fakeao3.WorkPage("x")}, KindWork, ClassOK},
		{"/works/11", fakeao3.Reply{Status: 404, Body: fakeao3.Page("Error 404")}, KindWork, ClassNotFound},
		{"/works/12", fakeao3.Reply{Status: 302, Header: map[string]string{"Location": "/users/login?restricted=true&return_to=%2Fworks%2F12"}}, KindWork, ClassRestricted},
		{"/works/13", fakeao3.Reply{Status: 302, Header: map[string]string{"Location": "/users/login?return_to=%2Fworks%2F13"}}, KindWork, ClassHidden},
		{"/works/14", fakeao3.Reply{Status: 302, Header: map[string]string{"Location": "/"}}, KindWork, ClassHidden},
		{"/works/15", fakeao3.Reply{Body: fakeao3.AdultGate(15)}, KindWork, ClassAdultGate},
		{"/works/16", fakeao3.Reply{Status: 403, Body: fakeao3.CloudflareChallenge}, KindWork, ClassUnavailable},
		{"/works/17", fakeao3.Reply{Body: fakeao3.CloudflareChallenge}, KindWork, ClassUnavailable},
		{"/works/18", fakeao3.Reply{Body: fakeao3.Maintenance}, KindWork, ClassUnavailable},
		// Real AO3 pages carry Cloudflare's injected detection script: still OK.
		{"/works/21", fakeao3.Reply{Body: strings.Replace(fakeao3.WorkPage("x"), "</body>",
			`<script src="/cdn-cgi/challenge-platform/scripts/jsd/main.js"></script></body>`, 1)}, KindWork, ClassOK},
		{"/series/22", fakeao3.Reply{Body: strings.Replace(fakeao3.Page("<p>x</p>"), "</body>",
			`<script>window.__CF$cv$params={};</script><script src="/cdn-cgi/challenge-platform/scripts/jsd/main.js"></script></body>`, 1)}, KindSeries, ClassOK},
		// Fanwork text can contain the classifier's marker strings; a real work page must still
		// classify OK (markers only add, and real work pages always have #workskin).
		{"/works/23", fakeao3.Reply{Body: fakeao3.WorkPage(`<p class="caution notice">fixture text with view_adult=true and ` +
			`<a href="/works/1?view_adult=true">a link</a></p>`)}, KindWork, ClassOK},
		{"/works/24", fakeao3.Reply{Body: fakeao3.WorkPage(`<p>fixture text: challenge-platform cf-wrapper cf-error-details ` +
			`cf-browser-verification __cf_chl_ waiting room id="outer" id="main"</p>`)}, KindWork, ClassOK},
		{"/tags/fixture2/works", fakeao3.Reply{Body: fakeao3.Page(`<blockquote class="userstuff summary"><p>fixture ` +
			`challenge-platform cf-wrapper class="caution notice" view_adult=true</p></blockquote>`)}, KindListing, ClassOK},
		{"/works/19", fakeao3.Reply{Body: fakeao3.Page("<p>no workskin</p>")}, KindWork, ClassUnexpected},
		{"/tags/fixture/works", fakeao3.Reply{Body: fakeao3.Page("")}, KindListing, ClassOK},
		{"/works/20", fakeao3.Reply{Status: 302, Header: map[string]string{"Location": "https://elsewhere.example/x"}}, KindWork, ClassUnexpected},
	}
	for _, c := range cases {
		h.srv.Handle(c.path, c.reply)
	}
	for _, c := range cases {
		r := h.get(t, c.kind, c.path)
		if r.Class != c.want {
			t.Errorf("%s: class %s (%s), want %s", c.path, r.Class, r.Reason, c.want)
		}
		// reset backoff noise between cases
		h.c.pacer.Report(ClassOK, 0)
	}
}

func TestRobotsUnavailableDisallowsAll(t *testing.T) {
	h := newHarness(t, 10*time.Second, 0)
	h.srv.SetRobots(fakeao3.Reply{Status: 525, Body: fakeao3.Cloudflare525}, fakeao3.Reply{Body: fakeao3.Robots})
	h.srv.Handle("/works/1", fakeao3.Reply{Body: fakeao3.WorkPage("x")})
	if _, err := h.c.Get(context.Background(), KindWork, "/works/1"); !errors.Is(err, ErrDisallowed) {
		t.Fatalf("want ErrDisallowed while robots unavailable, got %v", err)
	}
	for _, r := range h.srv.Requests() {
		if r.URI != "/robots.txt" {
			t.Fatalf("sent %s while robots unavailable", r.URI)
		}
	}
	h.clock.Sleep(context.Background(), robotsErrorTTL+time.Second)
	if r := h.get(t, KindWork, "/works/1"); r.Class != ClassOK {
		t.Fatalf("after robots recovered: %s", r.Class)
	}
}

func TestDailyBudget(t *testing.T) {
	h := newHarness(t, 10*time.Second, 0)
	h.c.pacer.budget = 3 // robots + 2 works
	h.srv.Handle("/works/1", fakeao3.Reply{Body: fakeao3.WorkPage("x")})
	h.get(t, KindWork, "/works/1")
	h.get(t, KindWork, "/works/1")
	day := h.clock.Now().UTC().Truncate(24 * time.Hour)
	h.get(t, KindWork, "/works/1")
	if last := h.recs[len(h.recs)-1].Time; last.Before(day.Add(24 * time.Hour)) {
		t.Fatalf("budget exceeded: request at %s before next day", last)
	}
}

func TestLogsAreContentFree(t *testing.T) {
	const sentinel = "SENTINELcontent"
	h := newHarness(t, 10*time.Second, 0)
	tagPath := "/tags/" + sentinel + "Tag/works"
	h.srv.Handle(tagPath, fakeao3.Reply{Body: fakeao3.Page("<p>" + sentinel + "</p>")})
	h.srv.Handle("/works/1", fakeao3.Reply{Body: fakeao3.WorkPage(sentinel)})
	h.srv.Handle("/works/2", fakeao3.Reply{Status: 429, Body: sentinel})
	h.get(t, KindListing, tagPath+"?work_search%5Bother_tag_names%5D="+sentinel+"&page=2")
	h.get(t, KindWork, "/works/1")
	h.get(t, KindWork, "/works/2")
	if strings.Contains(strings.ToLower(h.logs.String()), strings.ToLower(sentinel)) {
		t.Fatalf("sentinel leaked into logs:\n%s", h.logs.String())
	}
	for _, r := range h.recs {
		if strings.Contains(strings.ToLower(r.SafePath+r.Reason), strings.ToLower(sentinel)) {
			t.Fatalf("sentinel leaked into request record: %+v", r)
		}
	}
	if !strings.Contains(h.logs.String(), `"path":"/works/1"`) {
		t.Fatalf("numeric work paths should be logged verbatim:\n%s", h.logs.String())
	}
}

func TestSafePath(t *testing.T) {
	for in, want := range map[string]string{
		"/works/123/chapters/456":       "/works/123/chapters/456",
		"/works/1?view_full_work=true":  "/works/1?view_full_work=true",
		"/tags/x/works?page=3":          "/tags/" + Hash("x") + "/works?page=3",
		"/users/name/pseuds/name/works": "/users/" + Hash("name") + "/pseuds/" + Hash("name") + "/works",
	} {
		u, _ := h0().Resolve(in)
		if got := SafePath(u); got != want {
			t.Errorf("SafePath(%s) = %s, want %s", in, got, want)
		}
	}
}

func h0() *Client {
	c, _ := New(Options{BaseURL: "https://ao3.invalid", UserAgent: testUA})
	return c
}
