package imagefetch

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	_, err = st.Meta.Exec(`INSERT INTO works (id, first_seen) VALUES (1, 0)`)
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
	h := newHost(t)
	paths := []string{"/a.png", "/private/b.png", "/big.png", "/page", "/gone.png", "/flaky.png", "/dup.png", "/redir.png"}
	var urls []string
	for _, p := range paths {
		urls = append(urls, h.URL+p)
	}
	n, err := st.QueueImages(ctx, 1, urls)
	require.NoError(t, err)
	require.Len(t, urls, n)
	n, err = st.QueueImages(ctx, 1, urls)
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
		next, err := st.NextImageDue(ctx)
		require.NoError(t, err)
		if next.IsZero() {
			break
		}
		if wait <= 0 || wait > time.Hour {
			wait = next.Sub(clk) + time.Second
		}
		clk = clk.Add(wait)
	}
	counts, bytesOK, err := st.ImageCounts(ctx)
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
	_, err := st.QueueImages(ctx, 1, []string{h.URL + "/a.png"})
	require.NoError(t, err)
	w, err := New(Options{Store: st, UserAgent: testUA, Dir: t.TempDir(), MaxBytes: 1000})
	require.NoError(t, err)
	ran, _, err := w.Step(ctx)
	require.NoError(t, err)
	require.True(t, ran)
	counts, _, err := st.ImageCounts(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, counts["skipped"], "loopback image must be refused")
	require.Empty(t, h.seen, "loopback image must be refused before connecting")
}
