// Package imagefetch downloads images embedded in mirrored works so they render offline.
//
// Images live on external hosts (AO3 does not host images). Every request carries the same
// User-Agent as the AO3 client, honours each host's robots.txt, waits PerHostInterval between
// requests to the same host, runs one request at a time, accepts only image/* up to MaxBytes, and
// refuses private/loopback/link-local addresses (fanwork HTML is untrusted input). The client
// comes from httpx.NewExternalClient and never follows redirects itself: the worker follows up
// to three, checking each hop like a first request (never AO3, robots.txt, host interval).
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
	// Sleep waits out a redirect target's host interval mid-image (default: a real,
	// cancellable sleep; tests fake it).
	Sleep         func(ctx context.Context, d time.Duration) error
	Idle          time.Duration // poll interval when nothing is due (default 30s)
	AllowLoopback bool          // tests only
	MaxAttempts   int           // default 5
}

type Worker struct {
	o        Options
	client   *http.Client
	log      *slog.Logger
	mu       sync.Mutex
	nextHost map[string]time.Time
	robots   map[string]*httpx.Robots // by scheme://host
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
	if o.Sleep == nil {
		o.Sleep = sleep
	}
	client, err := httpx.NewExternalClient(httpx.ExternalOptions{UserAgent: o.UserAgent, DialControl: func(network, address string, _ syscall.RawConn) error {
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
	}})
	if err != nil {
		return nil, err
	}
	return &Worker{o: o, log: o.Logger.With("component", "imagefetch"), nextHost: map[string]time.Time{}, robots: map[string]*httpx.Robots{},
		client: client}, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Run downloads queued images until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	w.log.Info("image worker started", "per_host_interval", w.o.PerHostInterval.String(), "max_bytes", w.o.MaxBytes)
	for {
		ran, wait, err := w.Step(ctx)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // shutdown: a step error caused by cancellation is not a failure
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
	rows, err := w.images().NextImages(ctx, now, 50)
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
		w.safeFetch(ctx, r)
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

// safeFetch runs fetch and turns a panic into a failed image, so the worker goroutine (which
// would take the whole daemon down) keeps going. Only the panic's type is logged.
func (w *Worker) safeFetch(ctx context.Context, r store.ImageRow) {
	defer func() {
		if p := recover(); p != nil {
			w.log.Error("image fetch panicked", "image_id", r.ID, "panic_type", fmt.Sprintf("%T", p))
			_ = w.images().ImageFinal(context.WithoutCancel(ctx), r.ID, store.ImageFailed, "panic")
		}
	}()
	w.fetch(ctx, r)
}

func (w *Worker) fetch(ctx context.Context, r store.ImageRow) {
	log := w.log.With("image_id", r.ID, "host", httpx.Hash(r.Host), "attempt", r.Attempts+1)
	st := w.images()
	if isAO3(r.Host) {
		// AO3 traffic only goes through the paced AO3 client, never the image worker.
		log.Info("image on an AO3 host; skipped")
		_ = st.ImageFinal(ctx, r.ID, store.ImageSkipped, "ao3_host")
		return
	}
	u, err := url.Parse(r.URL)
	if err != nil {
		log.Info("image URL unparseable; failed")
		_ = st.ImageFinal(ctx, r.ID, store.ImageFailed, "bad_url")
		return
	}
	proceed, fetchedRobots := w.robotsGate(ctx, r, u, log)
	if !proceed || fetchedRobots {
		// Refused, or just spent a request on this host's robots.txt: the image waits for the
		// host interval (a later step).
		return
	}

	start := time.Now()
	resp, ok := w.follow(ctx, r, u, log)
	if !ok {
		return
	}
	defer resp.Body.Close()
	ct := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusForbidden:
		_ = st.ImageFinal(ctx, r.ID, store.ImageFailed, fmt.Sprintf("status=%d", resp.StatusCode))
		log.Info("image gone", "status", resp.StatusCode)
		return
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError:
		log.Warn("image host unavailable; will retry", "status", resp.StatusCode, "retry_after", resp.Header.Get("Retry-After"))
		_ = st.ImageRetry(ctx, r.ID, w.o.Now().Add(w.backoff(r.Attempts)), fmt.Sprintf("status=%d", resp.StatusCode), w.o.MaxAttempts)
		return
	case resp.StatusCode != http.StatusOK:
		log.Info("image failed", "status", resp.StatusCode)
		_ = st.ImageFinal(ctx, r.ID, store.ImageFailed, fmt.Sprintf("status=%d", resp.StatusCode))
		return
	case !strings.HasPrefix(ct, "image/"):
		_ = st.ImageFinal(ctx, r.ID, store.ImageSkipped, "content_type")
		log.Info("not an image", "content_type", ct)
		return
	case resp.ContentLength > w.o.MaxBytes:
		log.Info("image too large", "content_length", resp.ContentLength, "max_bytes", w.o.MaxBytes)
		_ = st.ImageFinal(ctx, r.ID, store.ImageTooLarge, fmt.Sprintf("content_length=%d", resp.ContentLength))
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, w.o.MaxBytes+1))
	if err != nil {
		log.Warn("image read failed; will retry", "err", short(err))
		_ = st.ImageRetry(ctx, r.ID, w.o.Now().Add(w.backoff(r.Attempts)), "read:"+short(err), w.o.MaxAttempts)
		return
	}
	if int64(len(body)) > w.o.MaxBytes {
		log.Info("image too large", "body_over", w.o.MaxBytes)
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

// maxRedirects caps the hops followed for one image.
const maxRedirects = 3

// follow requests u and follows redirects itself (the client never does), so every hop gets
// the checks the first request got: never an AO3 host, the hop host's robots.txt, and its
// per-host interval. When it returns ok=false the image's outcome is already recorded (or, on
// shutdown, the image is left queued).
func (w *Worker) follow(ctx context.Context, r store.ImageRow, u *url.URL, log *slog.Logger) (*http.Response, bool) {
	st := w.images()
	for hop := 0; ; hop++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		req.Header.Set("Accept", "image/*")
		resp, err := w.client.Do(req)
		w.hostDone(strings.ToLower(u.Hostname()))
		if err != nil {
			if errors.Is(err, errBlockedAddr) {
				log.Warn("image host resolves to a non-public address; skipped", "hop", hop)
				_ = st.ImageFinal(ctx, r.ID, store.ImageSkipped, "non_public_address")
				return nil, false
			}
			log.Warn("image request failed", "hop", hop, "err", short(err))
			_ = st.ImageRetry(ctx, r.ID, w.o.Now().Add(w.backoff(r.Attempts)), "transport:"+short(err), w.o.MaxAttempts)
			return nil, false
		}
		if !isRedirect(resp.StatusCode) {
			return resp, true
		}
		loc, lerr := u.Parse(resp.Header.Get("Location"))
		resp.Body.Close()
		switch {
		case lerr != nil || (loc.Scheme != "http" && loc.Scheme != "https") || loc.Host == "":
			log.Info("image redirect unusable; failed", "hop", hop)
			_ = st.ImageFinal(ctx, r.ID, store.ImageFailed, "bad_redirect")
		case hop >= maxRedirects:
			log.Info("too many image redirects; failed", "hop", hop)
			_ = st.ImageFinal(ctx, r.ID, store.ImageFailed, "too_many_redirects")
		case isAO3(loc.Hostname()):
			// The image worker never talks to AO3 (that request would bypass the AO3 pacer).
			log.Info("image redirects to an AO3 host; skipped", "hop", hop)
			_ = st.ImageFinal(ctx, r.ID, store.ImageSkipped, "ao3_redirect")
		default:
			host := strings.ToLower(loc.Hostname())
			log.Debug("following image redirect", "hop", hop+1, "to_host", httpx.Hash(host))
			if w.waitHost(ctx, host) != nil {
				return nil, false
			}
			proceed, fetchedRobots := w.robotsGate(ctx, r, loc, log)
			if !proceed || (fetchedRobots && w.waitHost(ctx, host) != nil) {
				return nil, false
			}
			u = loc
			continue
		}
		return nil, false
	}
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// waitHost waits until host's per-host interval has passed (a redirect target, mid-image).
func (w *Worker) waitHost(ctx context.Context, host string) error {
	w.mu.Lock()
	free := w.nextHost[host]
	w.mu.Unlock()
	if d := free.Sub(w.o.Now()); d > 0 {
		return w.o.Sleep(ctx, d)
	}
	return nil
}

// robotsGate checks u's host robots.txt for image r. proceed=false means the image's outcome
// is recorded (disallowed, refused, or retry later); fetched=true means a robots.txt request
// was just made to that host.
func (w *Worker) robotsGate(ctx context.Context, r store.ImageRow, u *url.URL, log *slog.Logger) (proceed, fetched bool) {
	st := w.images()
	allowed, fetched, retry, err := w.robotsAllowed(ctx, u, log)
	switch {
	case err != nil && errors.Is(err, errBlockedAddr):
		log.Warn("image host resolves to a non-public address; skipped")
		_ = st.ImageFinal(ctx, r.ID, store.ImageSkipped, "non_public_address")
	case err != nil && retry:
		_ = st.ImageRetry(ctx, r.ID, w.o.Now().Add(w.backoff(r.Attempts)), "robots:"+short(err), w.o.MaxAttempts)
	case err != nil:
		_ = st.ImageFinal(ctx, r.ID, store.ImageFailed, "robots:"+short(err))
	case !allowed:
		log.Info("image disallowed by host robots.txt")
		_ = st.ImageFinal(ctx, r.ID, store.ImageDisallowed, "robots")
	default:
		return true, fetched
	}
	return false, fetched
}

// imageStore is the worker's store access. Each image outcome is a single statement, so each
// call runs in its own transaction; HTTP never happens inside one.
type imageStore struct{ s *store.Store }

func (w *Worker) images() imageStore { return imageStore{s: w.o.Store} }

func (i imageStore) write(ctx context.Context, fn func(ctx context.Context, tx *store.Tx) error) error {
	return i.s.InTx(ctx, fn)
}

func (i imageStore) NextImages(ctx context.Context, now time.Time, limit int) ([]store.ImageRow, error) {
	return store.ReadValue(ctx, i.s, func(ctx context.Context, tx *store.ReadTx) ([]store.ImageRow, error) {
		return tx.NextImages(ctx, now, limit)
	})
}

func (i imageStore) ImageFinal(ctx context.Context, id int64, status, lastError string) error {
	return i.write(ctx, func(ctx context.Context, tx *store.Tx) error { return tx.ImageFinal(ctx, id, status, lastError) })
}

func (i imageStore) ImageRetry(ctx context.Context, id int64, notBefore time.Time, lastError string, maxAttempts int) error {
	return i.write(ctx, func(ctx context.Context, tx *store.Tx) error {
		return tx.ImageRetry(ctx, id, notBefore, lastError, maxAttempts)
	})
}

func (i imageStore) ImageDone(ctx context.Context, id int64, sha, contentType string, bytes int) error {
	return i.write(ctx, func(ctx context.Context, tx *store.Tx) error { return tx.ImageDone(ctx, id, sha, contentType, bytes) })
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
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
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
	if !e.Fresh(w.o.Now()) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, key+"/robots.txt", nil)
		resp, err := w.client.Do(req)
		w.hostDone(strings.ToLower(u.Hostname()))
		switch {
		case err != nil && errors.Is(err, errBlockedAddr):
			return false, false, false, errBlockedAddr
		case err != nil:
			return false, false, true, err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
		resp.Body.Close()
		e, err = httpx.NewRobots(resp.StatusCode, body, w.o.Now(), false)
		if err != nil {
			e, _ = httpx.NewRobots(http.StatusNotFound, nil, w.o.Now(), false) // unparseable: treat as no robots.txt
		}
		log.Debug("image host robots fetched", "status", resp.StatusCode)
		w.mu.Lock()
		w.robots[key] = e
		w.mu.Unlock()
		fetched = true
	}
	return e.Allowed(u), fetched, false, nil
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
