package httpx

import (
	"bytes"
	"context"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
		Logger: logger, Clock: h.clock, Rand: rand.NewPCG(42, 42),
		OnRequest: func(r RequestRecord) { h.recs = append(h.recs, r) },
	})
	require.NoError(t, err)
	h.c = c
	return h
}

func (h *harness) get(t *testing.T, kind Kind, ref string) *Response {
	t.Helper()
	r, err := h.c.Get(context.Background(), kind, ref)
	require.NoError(t, err, "Get %s", ref)
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
	require.Len(t, reqs, 4, "robots + 2 hops + listing")
	for _, r := range reqs {
		assert.Equal(t, testUA, r.UserAgent, "%s UA", r.URI)
		assert.Equal(t, "none", r.Header.Get("Sec-Fetch-Site"), "%s Sec-Fetch-Site", r.URI)
		// Only the truthful Sec-Fetch-Site: nothing that claims a browser tab or a human click.
		for _, k := range []string{"Sec-Fetch-User", "Sec-Fetch-Mode", "Sec-Fetch-Dest", "Upgrade-Insecure-Requests"} {
			assert.Empty(t, r.Header.Get(k), "%s sent %s", r.URI, k)
		}
	}
	assert.Equal(t, "/robots.txt", reqs[0].URI, "first request must be robots.txt")
}

func TestViewAdultCookieSent(t *testing.T) {
	h := newHarness(t, 10*time.Second, 0)
	h.srv.Handle("/works/7", fakeao3.Reply{Body: fakeao3.WorkPage("x")})
	h.get(t, KindWork, "/works/7?view_full_work=true")
	for _, r := range h.srv.Requests() {
		assert.Contains(t, r.Cookie, "view_adult=true", "%s: missing view_adult cookie", r.URI)
	}
}

func TestNewRequiresUA(t *testing.T) {
	for _, ua := range []string{"", "   ", "some-bot/1.0"} {
		_, err := New(Options{UserAgent: ua})
		assert.ErrorIs(t, err, ErrNoUA, "UA %q", ua)
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
		require.ErrorIs(t, err, ErrDisallowed, ref)
	}
	for _, r := range h.srv.Requests() {
		assert.Equal(t, "/robots.txt", r.URI, "disallowed URL was sent")
	}
	// Allowed forms we rely on.
	h.srv.Handle("/works/5", fakeao3.Reply{Body: fakeao3.WorkPage("x")})
	h.srv.Handle("/tags/fixture/works", fakeao3.Reply{Body: fakeao3.Page("")})
	for _, ref := range []string{
		"/works/5?view_full_work=true",
		"/tags/fixture/works?work_search%5Bsort_column%5D=revised_at&page=3",
	} {
		_, err := h.c.Get(context.Background(), KindPage, ref)
		require.NoError(t, err, "%s should be allowed", ref)
	}
}

func TestForeignHostRefused(t *testing.T) {
	h := newHarness(t, 10*time.Second, 0)
	_, err := h.c.Get(context.Background(), KindPage, "https://example.com/x")
	require.ErrorIs(t, err, ErrForeign)
	require.Empty(t, h.srv.Requests())
}

func TestPacingFloorAndInterval(t *testing.T) {
	h := newHarness(t, config.MinInterval, 0.5) // max jitter at the floor: must never go below 3s
	h.srv.Handle("/works/1", fakeao3.Reply{Body: fakeao3.WorkPage("x")})
	for range 20 {
		h.get(t, KindWork, "/works/1")
	}
	for i := 1; i < len(h.recs); i++ {
		require.GreaterOrEqual(t, h.recs[i].Time.Sub(h.recs[i-1].Time), config.MinInterval, "request %d below the floor", i)
	}

	h2 := newHarness(t, 10*time.Second, 0.2)
	h2.srv.Handle("/works/1", fakeao3.Reply{Body: fakeao3.WorkPage("x")})
	for range 20 {
		h2.get(t, KindWork, "/works/1")
	}
	for i := 1; i < len(h2.recs); i++ {
		gap := h2.recs[i].Time.Sub(h2.recs[i-1].Time)
		require.GreaterOrEqual(t, gap, 8*time.Second, "gap %d outside 10s +/-20%%", i)
		require.LessOrEqual(t, gap, 13*time.Second, "gap %d outside 10s +/-20%%", i)
	}
}

func TestRateLimitedHonorsRetryAfterAndSlowsDown(t *testing.T) {
	h := newHarness(t, 10*time.Second, 0)
	h.srv.Handle("/works/1",
		fakeao3.Reply{Status: 429, Header: map[string]string{"Retry-After": "300"}, Body: "slow down"},
		fakeao3.Reply{Body: fakeao3.WorkPage("x")})
	require.Equal(t, ClassRateLimited, h.get(t, KindWork, "/works/1").Class)
	before := h.clock.total()
	require.Equal(t, ClassOK, h.get(t, KindWork, "/works/1").Class)
	require.GreaterOrEqual(t, h.clock.total()-before, 300*time.Second, "must wait at least Retry-After")
	st := h.c.PacerState()
	require.InDelta(t, 1.5, st.Factor, 1e-9, "slowdown factor")
	require.Equal(t, 15*time.Second, st.Interval, "slowdown 1.5x => 15s")
}

func TestUnavailableBackoffAndBreaker(t *testing.T) {
	h := newHarness(t, 10*time.Second, 0)
	h.srv.Handle("/works/1", fakeao3.Reply{Status: 525, Body: fakeao3.Cloudflare525})
	var waits []time.Duration
	for i := range 5 {
		before := h.clock.total()
		r := h.get(t, KindWork, "/works/1")
		require.Equal(t, ClassUnavailable, r.Class, "attempt %d", i)
		require.Contains(t, r.Reason, "status=525", "attempt %d", i)
		waits = append(waits, h.clock.total()-before)
	}
	// waits[0] includes robots + first request spacing; from attempt 2 on: 1m, 2m, 4m, 8m.
	assert.Equal(t, []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute}, waits[1:], "backoffs")
	st := h.c.PacerState()
	require.True(t, st.BreakerOpen, "breaker should be open after 5 failures")
	require.Equal(t, 5, st.ConsecFails)
	h.srv.Handle("/works/1", fakeao3.Reply{Body: fakeao3.WorkPage("x")})
	before := h.clock.total()
	h.get(t, KindWork, "/works/1")
	require.GreaterOrEqual(t, h.clock.total()-before, 2*time.Hour, "breaker should hold 2h")
	require.Equal(t, 0, h.c.PacerState().ConsecFails, "success should reset failures")
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
		assert.Equal(t, c.want, r.Class, "%s (reason %s)", c.path, r.Reason)
		// reset backoff noise between cases
		h.c.pacer.Report(ClassOK, 0)
	}
}

func TestRobotsUnavailableDisallowsAll(t *testing.T) {
	h := newHarness(t, 10*time.Second, 0)
	h.srv.SetRobots(fakeao3.Reply{Status: 525, Body: fakeao3.Cloudflare525}, fakeao3.Reply{Body: fakeao3.Robots})
	h.srv.Handle("/works/1", fakeao3.Reply{Body: fakeao3.WorkPage("x")})
	_, err := h.c.Get(context.Background(), KindWork, "/works/1")
	require.ErrorIs(t, err, ErrDisallowed, "while robots unavailable")
	for _, r := range h.srv.Requests() {
		require.Equal(t, "/robots.txt", r.URI, "sent a request while robots unavailable")
	}
	require.NoError(t, h.clock.Sleep(context.Background(), robotsErrorTTL+time.Second))
	require.Equal(t, ClassOK, h.get(t, KindWork, "/works/1").Class, "after robots recovered")
}

// A rate-limited robots.txt is "busy", not "no robots.txt": nothing is allowed until it is
// re-read after the short error TTL (it must not become allow-all for a day).
func TestRobotsRateLimitedDisallowsAll(t *testing.T) {
	h := newHarness(t, 10*time.Second, 0)
	h.srv.SetRobots(fakeao3.Reply{Status: 429, Header: map[string]string{"Retry-After": "60"}}, fakeao3.Reply{Body: fakeao3.Robots})
	h.srv.Handle("/works/1", fakeao3.Reply{Body: fakeao3.WorkPage("x")})
	_, err := h.c.Get(context.Background(), KindWork, "/works/1")
	require.ErrorIs(t, err, ErrDisallowed, "429 robots.txt treated as allow-all")
	for _, r := range h.srv.Requests() {
		require.Equal(t, "/robots.txt", r.URI, "sent a request while robots was rate limited")
	}
	r, err := NewRobots(http.StatusTooManyRequests, nil, h.clock.Now(), false)
	require.NoError(t, err)
	require.True(t, r.Unavailable())
	require.False(t, r.Fresh(h.clock.Now().Add(robotsErrorTTL+time.Second)), "429 entry cached past the error TTL")
	require.NoError(t, h.clock.Sleep(context.Background(), robotsErrorTTL+time.Second))
	require.Equal(t, ClassOK, h.get(t, KindWork, "/works/1").Class, "after robots recovered")
}

func TestDailyBudget(t *testing.T) {
	h := newHarness(t, 10*time.Second, 0)
	h.c.pacer.budget = 3 // robots + 2 works
	h.srv.Handle("/works/1", fakeao3.Reply{Body: fakeao3.WorkPage("x")})
	h.get(t, KindWork, "/works/1")
	h.get(t, KindWork, "/works/1")
	day := h.clock.Now().UTC().Truncate(24 * time.Hour)
	h.get(t, KindWork, "/works/1")
	last := h.recs[len(h.recs)-1].Time
	require.False(t, last.Before(day.Add(24*time.Hour)), "budget exceeded: request at %s before next day", last)
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
	require.NotContains(t, strings.ToLower(h.logs.String()), strings.ToLower(sentinel), "sentinel leaked into logs")
	for _, r := range h.recs {
		require.NotContains(t, strings.ToLower(r.SafePath+r.Reason), strings.ToLower(sentinel), "sentinel leaked into request record")
	}
	require.Contains(t, h.logs.String(), `"path":"/works/1"`, "numeric work paths should be logged verbatim")
}

func TestSafePath(t *testing.T) {
	c, err := New(Options{BaseURL: "https://ao3.invalid", UserAgent: testUA})
	require.NoError(t, err)
	for in, want := range map[string]string{
		"/works/123/chapters/456":       "/works/123/chapters/456",
		"/works/1?view_full_work=true":  "/works/1?view_full_work=true",
		"/tags/x/works?page=3":          "/tags/" + Hash("x") + "/works?page=3",
		"/users/name/pseuds/name/works": "/users/" + Hash("name") + "/pseuds/" + Hash("name") + "/works",
	} {
		u, err := c.Resolve(in)
		require.NoError(t, err)
		assert.Equal(t, want, SafePath(u), "SafePath(%s)", in)
	}
}
