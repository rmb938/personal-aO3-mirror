// Package imagefetch downloads images embedded in mirrored works so they render offline.
//
// Images live on external hosts (AO3 does not host images). Every request carries the same
// User-Agent as the AO3 client, honours each host's robots.txt, waits PerHostInterval between
// requests to the same host, runs one request at a time, accepts only image/* up to MaxBytes, and
// refuses private/loopback/link-local addresses (fanwork HTML is untrusted input).
//
// Files are content-addressed: <dir>/ab/cd/<sha256>.<ext>. Logs carry image ids and hashed
// hosts only (image URLs can contain names).
package imagefetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/temoto/robotstxt"
	"golang.org/x/net/html"

	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// WorkImages lists embedded image URLs from a work's text (summary, notes and every chapter's
// body, notes, end notes and summary). Used by work fetches and by reparse.
func WorkImages(w model.Work) []string {
	frags := []string{w.SummaryHTML, w.NotesHTML, w.EndNotesHTML}
	for _, c := range w.Chapters {
		frags = append(frags, c.BodyHTML, c.NotesHTML, c.EndNotesHTML, c.SummaryHTML)
	}
	return Extract(frags...)
}

// Extract returns the absolute http(s) <img src> URLs in the given HTML fragments, deduplicated.
func Extract(fragments ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range fragments {
		if !strings.Contains(f, "<img") {
			continue
		}
		z := html.NewTokenizer(strings.NewReader(f))
		for {
			tt := z.Next()
			if tt == html.ErrorToken {
				break
			}
			if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
				continue
			}
			name, hasAttr := z.TagName()
			if string(name) != "img" || !hasAttr {
				continue
			}
			for {
				k, v, more := z.TagAttr()
				if string(k) == "src" {
					if u := normalize(string(v)); u != "" && !seen[u] {
						seen[u] = true
						out = append(out, u)
					}
				}
				if !more {
					break
				}
			}
		}
	}
	return out
}

func normalize(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	u.Fragment = ""
	return u.String()
}

func isAO3(host string) bool {
	h := strings.ToLower(host)
	return h == "archiveofourown.org" || strings.HasSuffix(h, ".archiveofourown.org") || h == "ao3.org" || strings.HasSuffix(h, ".ao3.org")
}

// Options configure a Worker.
type Options struct {
	Store           *store.Store
	UserAgent       string
	Dir             string // the mirror's image directory
	PerHostInterval time.Duration
	MaxBytes        int64
	Logger          *slog.Logger
	Now             func() time.Time
	Idle            time.Duration // poll interval when nothing is due (default 30s)
	AllowLoopback   bool          // tests only
	MaxAttempts     int           // default 5
}

type Worker struct {
	o        Options
	client   *http.Client
	log      *slog.Logger
	mu       sync.Mutex
	nextHost map[string]time.Time
	robots   map[string]*robotsEntry
}

type robotsEntry struct {
	data    *robotstxt.RobotsData
	fetched time.Time
	ttl     time.Duration
}

var errBlockedAddr = errors.New("imagefetch: refusing non-public address")

func New(o Options) (*Worker, error) {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Idle == 0 {
		o.Idle = 30 * time.Second
	}
	if o.MaxAttempts == 0 {
		o.MaxAttempts = 5
	}
	if o.PerHostInterval < time.Second {
		o.PerHostInterval = time.Second
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second, Control: func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return errBlockedAddr
		}
		if o.AllowLoopback && ip.IsLoopback() {
			return nil
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
			ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
			return errBlockedAddr
		}
		return nil
	}}
	base := &http.Transport{
		Proxy:                 nil, // direct only: the address check must see the real target
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConnsPerHost:   1,
	}
	rt, err := httpx.NewUATransport(o.UserAgent, base)
	if err != nil {
		return nil, err
	}
	return &Worker{o: o, log: o.Logger.With("component", "imagefetch"), nextHost: map[string]time.Time{}, robots: map[string]*robotsEntry{},
		client: &http.Client{Transport: rt, Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return errors.New("bad redirect scheme")
			}
			return nil
		}}}, nil
}

// Run downloads queued images until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	w.log.Info("image worker started", "per_host_interval", w.o.PerHostInterval.String(), "max_bytes", w.o.MaxBytes)
	for {
		ran, wait, err := w.Step(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			w.log.Error("image step failed", "err", err.Error())
			wait = w.o.Idle
		}
		if !ran {
			if wait <= 0 || wait > w.o.Idle {
				wait = w.o.Idle
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(wait):
			}
		}
	}
}

// Step downloads at most one due image whose host is not cooling down. When nothing can run it
// returns how long to wait.
func (w *Worker) Step(ctx context.Context) (bool, time.Duration, error) {
	now := w.o.Now()
	rows, err := w.o.Store.NextImages(ctx, now, 50)
	if err != nil {
		return false, 0, err
	}
	var earliest time.Time
	for _, r := range rows {
		w.mu.Lock()
		free := w.nextHost[r.Host]
		w.mu.Unlock()
		if free.After(now) {
			if earliest.IsZero() || free.Before(earliest) {
				earliest = free
			}
			continue
		}
		w.fetch(ctx, r)
		return true, 0, nil
	}
	if !earliest.IsZero() {
		return false, earliest.Sub(now), nil
	}
	return false, w.o.Idle, nil
}

func (w *Worker) hostDone(host string) {
	w.mu.Lock()
	w.nextHost[host] = w.o.Now().Add(w.o.PerHostInterval)
	w.mu.Unlock()
}

func (w *Worker) fetch(ctx context.Context, r store.ImageRow) {
	log := w.log.With("image_id", r.ID, "host", httpx.Hash(r.Host), "attempt", r.Attempts+1)
	st := w.o.Store
	if isAO3(r.Host) {
		_ = st.ImageFinal(ctx, r.ID, store.ImageSkipped, "ao3_host")
		return
	}
	u, err := url.Parse(r.URL)
	if err != nil {
		_ = st.ImageFinal(ctx, r.ID, store.ImageFailed, "bad_url")
		return
	}
	allowed, fetchedRobots, retry, err := w.robotsAllowed(ctx, u, log)
	switch {
	case err != nil && errors.Is(err, errBlockedAddr):
		log.Warn("image host resolves to a non-public address; skipped")
		_ = st.ImageFinal(ctx, r.ID, store.ImageSkipped, "non_public_address")
		return
	case err != nil && retry:
		_ = st.ImageRetry(ctx, r.ID, w.o.Now().Add(w.backoff(r.Attempts)), "robots:"+short(err), w.o.MaxAttempts)
		return
	case err != nil:
		_ = st.ImageFinal(ctx, r.ID, store.ImageFailed, "robots:"+short(err))
		return
	case !allowed:
		log.Info("image disallowed by host robots.txt")
		_ = st.ImageFinal(ctx, r.ID, store.ImageDisallowed, "robots")
		return
	case fetchedRobots:
		// Just spent a request on this host's robots.txt: the image waits for the host interval.
		return
	}

	start := time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, r.URL, nil)
	req.Header.Set("Accept", "image/*")
	resp, err := w.client.Do(req)
	w.hostDone(r.Host)
	if err != nil {
		if errors.Is(err, errBlockedAddr) {
			log.Warn("image host resolves to a non-public address; skipped")
			_ = st.ImageFinal(ctx, r.ID, store.ImageSkipped, "non_public_address")
			return
		}
		log.Warn("image request failed", "err", short(err))
		_ = st.ImageRetry(ctx, r.ID, w.o.Now().Add(w.backoff(r.Attempts)), "transport:"+short(err), w.o.MaxAttempts)
		return
	}
	defer resp.Body.Close()
	ct := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	switch {
	case resp.StatusCode == 404 || resp.StatusCode == 410 || resp.StatusCode == 403:
		_ = st.ImageFinal(ctx, r.ID, store.ImageFailed, fmt.Sprintf("status=%d", resp.StatusCode))
		log.Info("image gone", "status", resp.StatusCode)
		return
	case resp.StatusCode == 429 || resp.StatusCode >= 500:
		_ = st.ImageRetry(ctx, r.ID, w.o.Now().Add(w.backoff(r.Attempts)), fmt.Sprintf("status=%d", resp.StatusCode), w.o.MaxAttempts)
		return
	case resp.StatusCode != 200:
		_ = st.ImageFinal(ctx, r.ID, store.ImageFailed, fmt.Sprintf("status=%d", resp.StatusCode))
		return
	case !strings.HasPrefix(ct, "image/"):
		_ = st.ImageFinal(ctx, r.ID, store.ImageSkipped, "content_type")
		log.Info("not an image", "content_type", ct)
		return
	case resp.ContentLength > w.o.MaxBytes:
		_ = st.ImageFinal(ctx, r.ID, store.ImageTooLarge, fmt.Sprintf("content_length=%d", resp.ContentLength))
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, w.o.MaxBytes+1))
	if err != nil {
		_ = st.ImageRetry(ctx, r.ID, w.o.Now().Add(w.backoff(r.Attempts)), "read:"+short(err), w.o.MaxAttempts)
		return
	}
	if int64(len(body)) > w.o.MaxBytes {
		_ = st.ImageFinal(ctx, r.ID, store.ImageTooLarge, "body")
		return
	}
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	if err := w.write(sha, ct, body); err != nil {
		log.Error("image write failed", "err", err.Error())
		_ = st.ImageRetry(ctx, r.ID, w.o.Now().Add(time.Hour), "write", w.o.MaxAttempts)
		return
	}
	if err := st.ImageDone(ctx, r.ID, sha, ct, len(body)); err != nil {
		log.Error("image record failed", "err", err.Error())
		return
	}
	log.Info("image stored", "bytes", len(body), "content_type", ct, "ms", time.Since(start).Milliseconds())
}

// Path returns where an image with this hash and content type is stored.
func Path(dir, sha, contentType string) string {
	return filepath.Join(dir, sha[:2], sha[2:4], sha+ext(contentType))
}

func ext(ct string) string {
	switch ct {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	}
	if exts, _ := mime.ExtensionsByType(ct); len(exts) > 0 {
		return exts[0]
	}
	return ".img"
}

func (w *Worker) write(sha, ct string, body []byte) error {
	p := Path(w.o.Dir, sha, ct)
	if _, err := os.Stat(p); err == nil {
		return nil // content-addressed: already have it
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (w *Worker) backoff(attempts int) time.Duration {
	d := 10 * time.Minute << attempts
	if d > 24*time.Hour || d <= 0 {
		d = 24 * time.Hour
	}
	return d
}

// robotsAllowed checks the image host's robots.txt (fetched with our UA, cached 24h).
// retry=true means the check could not be made now (5xx/network) and should be retried.
func (w *Worker) robotsAllowed(ctx context.Context, u *url.URL, log *slog.Logger) (allowed, fetched, retry bool, err error) {
	key := u.Scheme + "://" + u.Host
	w.mu.Lock()
	e := w.robots[key]
	w.mu.Unlock()
	if e == nil || w.o.Now().Sub(e.fetched) > e.ttl {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, key+"/robots.txt", nil)
		resp, err := w.client.Do(req)
		w.hostDone(strings.ToLower(u.Hostname()))
		ttl := 24 * time.Hour
		var data *robotstxt.RobotsData
		switch {
		case err != nil && errors.Is(err, errBlockedAddr):
			return false, false, false, errBlockedAddr
		case err != nil:
			return false, false, true, err
		default:
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
			resp.Body.Close()
			data, err = robotstxt.FromStatusAndBytes(resp.StatusCode, body)
			if err != nil {
				data, _ = robotstxt.FromStatusAndBytes(404, nil) // unparseable: treat as no robots.txt
			}
			if resp.StatusCode >= 500 {
				ttl = 15 * time.Minute
			}
			log.Debug("image host robots fetched", "status", resp.StatusCode)
		}
		e = &robotsEntry{data: data, fetched: w.o.Now(), ttl: ttl}
		w.mu.Lock()
		w.robots[key] = e
		w.mu.Unlock()
		fetched = true
	}
	target := u.EscapedPath()
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	return e.data.TestAgent(target, httpx.ProductToken), fetched, false, nil
}

func short(err error) string {
	s := err.Error()
	// Drop URLs from net/http error strings (they can contain names).
	if i := strings.Index(s, "\""); i >= 0 {
		if j := strings.LastIndex(s, "\""); j > i {
			s = s[:i] + "<url>" + s[j+1:]
		}
	}
	if len(s) > 100 {
		s = s[:100]
	}
	return s
}
