package imagefetch

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

const testUA = "personal-ao3-mirror/test (personal offline archive; contact: discord @fixture_user)"

func TestExtract(t *testing.T) {
	got := Extract(`<p>x <img src="https://img.example.invalid/a.png" alt="x"> <img src='/rel.png'>
		<img src="data:image/png;base64,AAAA"> <IMG SRC="http://img.example.invalid/b.jpg#frag"/></p>`,
		`<img src="https://img.example.invalid/a.png">`, `no images`)
	want := []string{"https://img.example.invalid/a.png", "http://img.example.invalid/b.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

type hostServer struct {
	*httptest.Server
	mu    sync.Mutex
	seen  []string
	uas   []string
	flaky int
}

func newHost(t *testing.T) *hostServer {
	h := &hostServer{}
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 100)...)
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.seen = append(h.seen, r.URL.Path)
		h.uas = append(h.uas, r.UserAgent())
		h.mu.Unlock()
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nDisallow: /private/\n")
		case "/a.png", "/private/b.png", "/dup.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(png)
		case "/big.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(bytes.Repeat([]byte{2}, 5000))
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html></html>")
		case "/redir.png":
			// Redirect to an AO3 host: CheckRedirect must refuse before any request is made.
			http.Redirect(w, r, "https://archiveofourown.org/images/x.png", http.StatusFound)
		case "/flaky.png":
			h.mu.Lock()
			h.flaky++
			n := h.flaky
			h.mu.Unlock()
			if n == 1 {
				w.WriteHeader(503)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			w.Write(png)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(h.Close)
	return h
}

func TestWorker(t *testing.T) {
	ctx := context.Background()
	clk := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clk }
	dir := t.TempDir()
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	st, err := store.Open(ctx, filepath.Join(dir, "m"), store.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := newHost(t)
	paths := []string{"/a.png", "/private/b.png", "/big.png", "/page", "/gone.png", "/flaky.png", "/dup.png", "/redir.png"}
	var urls []string
	for _, p := range paths {
		urls = append(urls, h.URL+p)
	}
	st.Meta.Exec(`INSERT INTO works (id, first_seen) VALUES (1, 0)`)
	if n, err := st.QueueImages(ctx, 1, urls); err != nil || n != len(urls) {
		t.Fatal(n, err)
	}
	if n, _ := st.QueueImages(ctx, 1, urls); n != 0 {
		t.Fatal("re-queue must not duplicate")
	}

	w, err := New(Options{Store: st, UserAgent: testUA, Dir: filepath.Join(dir, "img"), PerHostInterval: 3 * time.Second,
		MaxBytes: 1000, Logger: log, Now: now, AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	var lastFetch time.Time
	for i := 0; i < 200; i++ {
		h.mu.Lock()
		before := len(h.seen)
		h.mu.Unlock()
		ran, wait, err := w.Step(ctx)
		if err != nil {
			t.Fatal(err)
		}
		h.mu.Lock()
		requested := len(h.seen) - before
		h.mu.Unlock()
		if requested > 1 {
			t.Fatalf("one step made %d requests to the same host", requested)
		}
		if requested == 1 {
			if !lastFetch.IsZero() && clk.Sub(lastFetch) < 3*time.Second {
				t.Fatalf("per-host interval violated: %s", clk.Sub(lastFetch))
			}
			lastFetch = clk
		}
		if ran {
			continue
		}
		next, _ := st.NextImageDue(ctx)
		if next.IsZero() {
			break
		}
		if wait <= 0 || wait > time.Hour {
			wait = next.Sub(clk) + time.Second
		}
		clk = clk.Add(wait)
	}
	counts, bytesOK, _ := st.ImageCounts(ctx)
	// skipped: /page (not an image) and /redir.png (redirects to AO3, refused before following).
	want := map[string]int{"ok": 3, "disallowed": 1, "too_large": 1, "skipped": 2, "failed": 1}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("counts %v want %v", counts, want)
	}
	if bytesOK != 3*108 {
		t.Fatalf("bytes %d", bytesOK)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "img", "*", "*", "*.png"))
	if len(files) != 1 { // a.png, flaky.png and dup.png are identical bytes: one file
		t.Fatalf("content-addressed files: %v", files)
	}
	for _, p := range h.seen {
		if p == "/private/b.png" {
			t.Fatal("robots-disallowed image was requested")
		}
	}
	for _, ua := range h.uas {
		if ua != testUA {
			t.Fatalf("request without UA: %q", ua)
		}
	}
	if strings.Contains(logs.String(), "/a.png") || strings.Contains(logs.String(), "127.0.0.1") {
		t.Fatalf("logs leaked image URL/host:\n%s", logs.String())
	}
}

func TestBlocksNonPublicAddresses(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := newHost(t)
	st.Meta.Exec(`INSERT INTO works (id, first_seen) VALUES (1, 0)`)
	st.QueueImages(ctx, 1, []string{h.URL + "/a.png"})
	w, _ := New(Options{Store: st, UserAgent: testUA, Dir: t.TempDir(), MaxBytes: 1000})
	if ran, _, err := w.Step(ctx); err != nil || !ran {
		t.Fatal(ran, err)
	}
	counts, _, _ := st.ImageCounts(ctx)
	if counts["skipped"] != 1 || len(h.seen) != 0 {
		t.Fatalf("loopback image must be refused before connecting: %v seen=%v", counts, h.seen)
	}
	_ = os.Getpid
}
