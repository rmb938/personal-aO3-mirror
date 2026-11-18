package imagefetch

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

const testUA = "personal-ao3-mirror/test (personal offline archive; contact: discord @fixture_user)"

func TestExtract(t *testing.T) {
	got := Extract(`<p>x <img src="https://img.example.invalid/a.png" alt="x"> <img src='/rel.png'>
		<img src="data:image/png;base64,AAAA"> <IMG SRC="http://img.example.invalid/b.jpg#frag"/></p>`,
		`<img src="https://img.example.invalid/a.png">`, `no images`)
	require.Equal(t, []string{"https://img.example.invalid/a.png", "http://img.example.invalid/b.jpg"}, got)
}

type hostServer struct {
	*httptest.Server
	mu    sync.Mutex
	seen  []string
	uas   []string
	flaky int
}

func newHost(t *testing.T) *hostServer {
	t.Helper()
	h := &hostServer{}
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 100)...)
	write := func(w http.ResponseWriter, b []byte) {
		if _, err := w.Write(b); err != nil {
			t.Errorf("fake image host write: %v", err)
		}
	}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.seen = append(h.seen, r.URL.Path)
		h.uas = append(h.uas, r.UserAgent())
		h.mu.Unlock()
		switch r.URL.Path {
		case "/robots.txt":
			write(w, []byte("User-agent: *\nDisallow: /private/\n"))
		case "/a.png", "/private/b.png", "/dup.png":
			w.Header().Set("Content-Type", "image/png")
			write(w, png)
		case "/big.png":
			w.Header().Set("Content-Type", "image/png")
			write(w, bytes.Repeat([]byte{2}, 5000))
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			write(w, []byte("<html></html>"))
		case "/redir.png":
			// Redirect to an AO3 host: CheckRedirect must refuse before any request is made.
			http.Redirect(w, r, "https://archiveofourown.org/images/x.png", http.StatusFound)
		case "/flaky.png":
			h.mu.Lock()
			h.flaky++
			n := h.flaky
			h.mu.Unlock()
			if n == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			write(w, png)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(h.Close)
	return h
}

func openStore(t *testing.T, dir string, now func() time.Time) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), dir, store.Options{Now: now})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	_, err = st.AutoCommitForTestOnly().UpsertWork(context.Background(), model.Work{ID: 1}, model.FromBlurb, 0)
	require.NoError(t, err)
	return st
}

func TestWorker(t *testing.T) {
	ctx := context.Background()
	clk := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clk }
	dir := t.TempDir()
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	st := openStore(t, filepath.Join(dir, "m"), now)
	db := st.AutoCommitForTestOnly()
	h := newHost(t)
	paths := []string{"/a.png", "/private/b.png", "/big.png", "/page", "/gone.png", "/flaky.png", "/dup.png", "/redir.png"}
	var urls []string
	for _, p := range paths {
		urls = append(urls, h.URL+p)
	}
	n, err := db.QueueImages(ctx, 1, urls)
	require.NoError(t, err)
	require.Len(t, urls, n)
	n, err = db.QueueImages(ctx, 1, urls)
	require.NoError(t, err)
	require.Equal(t, 0, n, "re-queue must not duplicate")

	w, err := New(Options{Store: st, UserAgent: testUA, Dir: filepath.Join(dir, "img"), PerHostInterval: 3 * time.Second,
		MaxBytes: 1000, Logger: log, Now: now, AllowLoopback: true})
	require.NoError(t, err)
	var lastFetch time.Time
	for range 200 {
		h.mu.Lock()
		before := len(h.seen)
		h.mu.Unlock()
		ran, wait, err := w.Step(ctx)
		require.NoError(t, err)
		h.mu.Lock()
		requested := len(h.seen) - before
		h.mu.Unlock()
		require.LessOrEqual(t, requested, 1, "one step made several requests to the same host")
		if requested == 1 {
			if !lastFetch.IsZero() {
				require.GreaterOrEqual(t, clk.Sub(lastFetch), 3*time.Second, "per-host interval violated")
			}
			lastFetch = clk
		}
		if ran {
			continue
		}
		next, err := db.NextImageDueForTestOnly(ctx)
		require.NoError(t, err)
		if next.IsZero() {
			break
		}
		if wait <= 0 || wait > time.Hour {
			wait = next.Sub(clk) + time.Second
		}
		clk = clk.Add(wait)
	}
	counts, bytesOK, err := db.ImageCounts(ctx)
	require.NoError(t, err)
	// skipped: /page (not an image) and /redir.png (redirects to AO3, refused before following).
	require.Equal(t, map[string]int{"ok": 3, "disallowed": 1, "too_large": 1, "skipped": 2, "failed": 1}, counts)
	require.EqualValues(t, 3*108, bytesOK)
	files, err := filepath.Glob(filepath.Join(dir, "img", "*", "*", "*.png"))
	require.NoError(t, err)
	require.Len(t, files, 1, "a.png, flaky.png and dup.png are identical bytes: one content-addressed file")
	require.NotContains(t, h.seen, "/private/b.png", "robots-disallowed image was requested")
	for _, ua := range h.uas {
		require.Equal(t, testUA, ua, "every request carries the UA")
	}
	require.NotContains(t, logs.String(), "/a.png", "logs leaked image URL")
	require.NotContains(t, logs.String(), "127.0.0.1", "logs leaked image host")
}

func TestBlocksNonPublicAddresses(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, t.TempDir(), nil)
	h := newHost(t)
	_, err := st.AutoCommitForTestOnly().QueueImages(ctx, 1, []string{h.URL + "/a.png"})
	require.NoError(t, err)
	w, err := New(Options{Store: st, UserAgent: testUA, Dir: t.TempDir(), MaxBytes: 1000})
	require.NoError(t, err)
	ran, _, err := w.Step(ctx)
	require.NoError(t, err)
	require.True(t, ran)
	counts, _, err := st.AutoCommitForTestOnly().ImageCounts(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, counts["skipped"], "loopback image must be refused")
	require.Empty(t, h.seen, "loopback image must be refused before connecting")
}

// A redirect to another host is checked like a first request: that host's robots.txt is
// fetched and obeyed, and its per-host interval is waited out.
func TestRedirectChecksTargetHostRobots(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	clk := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clk }
	var slept time.Duration
	fakeSleep := func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		clk, slept = clk.Add(d), slept+d
		return nil
	}
	b := newHost(t) // robots.txt disallows /private/
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			w.WriteHeader(http.StatusNotFound)
		case "/to-private.png":
			http.Redirect(w, r, b.URL+"/private/b.png", http.StatusFound)
		case "/to-ok.png":
			http.Redirect(w, r, b.URL+"/a.png", http.StatusFound)
		case "/loop.png":
			http.Redirect(w, r, "/loop.png", http.StatusFound)
		}
	}))
	t.Cleanup(a.Close)
	st := openStore(t, t.TempDir(), now)
	db := st.AutoCommitForTestOnly()
	_, err := db.QueueImages(ctx, 1, []string{a.URL + "/to-private.png", a.URL + "/to-ok.png", a.URL + "/loop.png"})
	require.NoError(t, err)
	w, err := New(Options{Store: st, UserAgent: testUA, Dir: t.TempDir(), PerHostInterval: 3 * time.Second,
		MaxBytes: 1000, Now: now, Sleep: fakeSleep, AllowLoopback: true})
	require.NoError(t, err)
	for range 50 {
		ran, wait, err := w.Step(ctx)
		require.NoError(t, err)
		if !ran {
			next, err := db.NextImageDueForTestOnly(ctx)
			require.NoError(t, err)
			if next.IsZero() {
				break
			}
			require.NoError(t, fakeSleep(ctx, max(wait, time.Second)))
		}
	}
	counts, _, err := db.ImageCounts(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"ok": 1, "disallowed": 1, "failed": 1}, counts)
	b.mu.Lock()
	defer b.mu.Unlock()
	require.Contains(t, b.seen, "/robots.txt", "redirect target's robots.txt not checked")
	require.NotContains(t, b.seen, "/private/b.png", "redirect followed into a robots-disallowed path")
	require.Positive(t, slept, "redirect hops did not wait for the host interval")
}

// fakeWorker builds a worker for urls on a fake clock whose Sleep advances it. It returns the
// store and a driver that runs steps, advancing the clock by at most a minute whenever nothing
// is due.
func fakeWorker(t *testing.T, urls []string) (*store.Tx, func(steps int)) {
	t.Helper()
	ctx := context.Background()
	var mu sync.Mutex
	clk := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clk }
	advance := func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		clk = clk.Add(d)
		return nil
	}
	st := openStore(t, t.TempDir(), now)
	db := st.AutoCommitForTestOnly()
	_, err := db.QueueImages(ctx, 1, urls)
	require.NoError(t, err)
	w, err := New(Options{Store: st, UserAgent: testUA, Dir: t.TempDir(), PerHostInterval: 3 * time.Second,
		MaxBytes: 1000, Now: now, Sleep: advance, AllowLoopback: true})
	require.NoError(t, err)
	run := func(steps int) {
		for range steps {
			ran, wait, err := w.Step(ctx)
			require.NoError(t, err)
			if !ran {
				require.NoError(t, advance(ctx, min(max(wait, time.Second), time.Minute)))
			}
		}
	}
	return db, run
}

// A 429 holds back the whole host for its Retry-After, not just the one image.
func TestHostBacksOffOn429(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/robots.txt":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusTooManyRequests)
		}
	}))
	t.Cleanup(h.Close)
	var urls []string
	for _, p := range []string{"/1.png", "/2.png", "/3.png", "/4.png"} {
		urls = append(urls, h.URL+p)
	}
	_, run := fakeWorker(t, urls)
	run(30) // at most 30 minutes of fake time: well inside the hour-long Retry-After
	mu.Lock()
	defer mu.Unlock()
	images := 0
	for _, p := range seen {
		if p != "/robots.txt" {
			images++
		}
	}
	require.Equal(t, 1, images, "host kept being requested during its Retry-After (%v)", seen)
}

// pngHost serves robots.txt from robots (in order, last repeats) and a PNG for anything else.
func pngHost(t *testing.T, robots ...int) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			mu.Lock()
			status := robots[0]
			if len(robots) > 1 {
				robots = robots[1:]
			}
			mu.Unlock()
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 50)...))
	}))
	t.Cleanup(h.Close)
	return h
}

// A robots.txt that is briefly unavailable (503) refuses images only until it is read again:
// the image is retried and downloaded, not marked disallowed for good.
func TestRobotsUnavailableRetriesImage(t *testing.T) {
	h := pngHost(t, http.StatusServiceUnavailable, http.StatusNotFound)
	db, run := fakeWorker(t, []string{h.URL + "/a.png"})
	run(60) // up to an hour of fake time: past the 15 minute robots error TTL
	counts, _, err := db.ImageCounts(context.Background())
	require.NoError(t, err)
	require.Equal(t, map[string]int{"ok": 1}, counts, "image lost to a temporary robots.txt error")
}

// A host held back by a 429 does not stall images on other hosts, however many of its own
// images are queued ahead of them.
func TestBackedOffHostDoesNotBlockOthers(t *testing.T) {
	busy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Retry-After", "21600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(busy.Close)
	other := pngHost(t, http.StatusNotFound)
	var urls []string
	for i := range 80 { // more than any fixed look-ahead window
		urls = append(urls, busy.URL+"/"+strconv.Itoa(i)+".png")
	}
	// Same server, other hostname: localhost vs 127.0.0.1 are different hosts to the worker.
	urls = append(urls, strings.Replace(other.URL, "127.0.0.1", "localhost", 1)+"/x.png")
	db, run := fakeWorker(t, urls)
	run(20)
	counts, _, err := db.ImageCounts(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, counts["ok"], "other host's image stalled behind a backed-off host (%v)", counts)
}

func TestIsAO3TrailingDot(t *testing.T) {
	require.True(t, isAO3("archiveofourown."+"org."), "fully-qualified AO3 host")
	require.True(t, isAO3("WWW.ArchiveOfOurOwn."+"org."))
	require.False(t, isAO3("example.org."))
}

// A robots.txt that redirects (http→https is common) is followed, and its rules apply.
func TestRobotsRedirectFollowed(t *testing.T) {
	b := newHost(t) // robots.txt disallows /private/
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			http.Redirect(w, r, b.URL+"/robots.txt", http.StatusMovedPermanently)
		default:
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 50)...))
		}
	}))
	t.Cleanup(a.Close)
	db, run := fakeWorker(t, []string{a.URL + "/private/x.png", a.URL + "/ok.png"})
	run(20)
	counts, _, err := db.ImageCounts(context.Background())
	require.NoError(t, err)
	require.Equal(t, map[string]int{"ok": 1, "disallowed": 1}, counts, "redirected robots.txt not applied")
}

// queuedAttempts returns the attempts of every still-queued image.
func queuedAttempts(t *testing.T, db *store.Tx) []int {
	t.Helper()
	rows, err := db.NextImages(context.Background(), time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), 100, nil)
	require.NoError(t, err)
	var out []int
	for _, r := range rows {
		out = append(out, r.Attempts)
	}
	return out
}

// A redirect into a host that is backing off defers the image to when the host is free, instead
// of sleeping the single worker through the backoff, and uses no attempt.
func TestRedirectToBackedOffHostDefers(t *testing.T) {
	busy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Retry-After", "21600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(busy.Close)
	busyURL := strings.Replace(busy.URL, "127.0.0.1", "localhost", 1) // another host than the redirector
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		http.Redirect(w, r, busyURL+"/target.png", http.StatusFound)
	}))
	t.Cleanup(redir.Close)
	db, run := fakeWorker(t, []string{busyURL + "/first.png", redir.URL + "/hop.png"})
	run(10) // ten steps: minutes of fake time, far less than the 6h backoff
	require.Equal(t, []int{1, 0}, queuedAttempts(t, db), "first: 429 retry; hop: deferred without an attempt")
}

// A host whose robots.txt stays unavailable never burns its images' attempts, and its
// robots.txt Retry-After is honoured (not re-read every error TTL).
func TestLongRobotsOutageKeepsImages(t *testing.T) {
	var mu sync.Mutex
	robotsReads := 0
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/robots.txt" {
			robotsReads++
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		t.Errorf("image requested while robots.txt was unavailable: %s", r.URL.Path)
	}))
	t.Cleanup(h.Close)
	db, run := fakeWorker(t, []string{h.URL + "/a.png", h.URL + "/b.png"})
	run(50) // under an hour of fake time
	mu.Lock()
	require.Equal(t, 1, robotsReads, "robots.txt re-read during its Retry-After")
	mu.Unlock()
	require.Equal(t, []int{0, 0}, queuedAttempts(t, db), "robots outage used up image attempts")
	run(400) // hours of fake time: the outage continues
	require.Equal(t, []int{0, 0}, queuedAttempts(t, db), "long robots outage failed the images")
}

func TestImageHostNormalised(t *testing.T) {
	u, err := url.Parse("https://Img.Example.ORG./a.png")
	require.NoError(t, err)
	require.Equal(t, "img.example.org", store.ImageHost(u))
}

// After the last download the worker sleeps for Idle, not just until the finished host's
// interval ends (that host has nothing left to fetch).
func TestIdleAfterLastImage(t *testing.T) {
	ctx := context.Background()
	h := pngHost(t, http.StatusNotFound)
	clk := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clk }
	st := openStore(t, t.TempDir(), now)
	_, err := st.AutoCommitForTestOnly().QueueImages(ctx, 1, []string{h.URL + "/a.png"})
	require.NoError(t, err)
	w, err := New(Options{Store: st, UserAgent: testUA, Dir: t.TempDir(), PerHostInterval: 3 * time.Second,
		MaxBytes: 1000, Now: now, AllowLoopback: true, Idle: time.Minute})
	require.NoError(t, err)
	for range 2 { // robots.txt, then the image
		ran, _, err := w.Step(ctx)
		require.NoError(t, err)
		require.True(t, ran)
		clk = clk.Add(5 * time.Second)
	}
	clk = clk.Add(-4 * time.Second) // the host is still inside its interval
	ran, wait, err := w.Step(ctx)
	require.NoError(t, err)
	require.False(t, ran)
	require.Equal(t, time.Minute, wait, "woke for a host with nothing queued")
}
