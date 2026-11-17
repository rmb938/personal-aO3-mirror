// Package httpx is the ONLY way ao3mirror talks to AO3. It guarantees, for every request:
//   - the User-Agent with the configured contact,
//   - robots.txt compliance (checked before sending; disallowed URLs are never sent),
//   - one request in flight, paced at the configured interval (floor 3s) with jitter,
//   - backoff on 429 / 5xx / Cloudflare pages and a circuit breaker,
//   - content-free, debug-friendly logging.
package httpx

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/logx"
)

var (
	ErrDisallowed = errors.New("httpx: disallowed by robots.txt")
	ErrForeign    = errors.New("httpx: URL is not on the configured AO3 host")
	ErrTooLarge   = errors.New("httpx: response body exceeds limit")
	ErrNoUA       = errors.New("httpx: user agent is required")

	errNoDialControl = errors.New("httpx: external client needs a DialControl")
)

const (
	DefaultBaseURL = "https://archiveofourown.org"
	maxRedirects   = 3
	defaultMaxBody = 64 << 20
)

// Options configures a Client.
type Options struct {
	BaseURL     string // defaults to DefaultBaseURL; tests point it at fakeao3
	UserAgent   string // required; build with config.Config.UserAgent
	Interval    time.Duration
	Jitter      float64
	DailyBudget int
	MaxBody     int64
	Logger      *slog.Logger
	Clock       Clock               // defaults to real time
	Transport   http.RoundTripper   // defaults to a tuned http.Transport
	OnRequest   func(RequestRecord) // optional: persist to the requests table
	Rand        RandSource          // jitter and request-ID source; nil = crypto/rand (tests pass a seeded PCG)
}

// RequestRecord is a content-free summary of one HTTP exchange.
type RequestRecord struct {
	ReqID    string
	Time     time.Time
	Kind     Kind
	SafePath string
	Status   int
	Class    Class
	Reason   string
	Bytes    int
	Duration time.Duration
	Waited   time.Duration
	Hop      int
	JobID    int64 // from WithJobID; 0 = none
}

type jobIDKey struct{}

// WithJobID tags requests made with ctx with a scheduler job id (for the request log).
func WithJobID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, jobIDKey{}, id)
}

func jobIDFrom(ctx context.Context) int64 {
	id, _ := ctx.Value(jobIDKey{}).(int64)
	return id
}

// Response is a fully read AO3 response.
type Response struct {
	ReqID  string
	Kind   Kind
	URL    *url.URL // final URL after followed redirects
	Status int
	Header http.Header
	Body   []byte
	Class  Class
	Reason string
}

// Client talks to one AO3 host.
type Client struct {
	base    *url.URL
	ua      string
	http    *http.Client
	pacer   *Pacer
	clock   Clock
	log     *slog.Logger
	maxBody int64
	onReq   func(RequestRecord)

	mu     sync.Mutex // serializes requests (one in flight)
	robots *Robots
	seq    atomic.Uint64
	idPfx  string
}

// uaTransport forces the User-Agent on every outgoing request, whatever the caller set.
type uaTransport struct {
	ua   string
	next http.RoundTripper
}

// fetchSiteNone is the Fetch Metadata value for a request not initiated from any web page.
// That is literally true for the daemon (no referrer, never cross-site). During AO3's
// Cloudflare 525 periods, requests without Sec-Fetch-Site were failing while ones with it got
// through. Only this one, truthful header is sent: never Sec-Fetch-User/Mode/Dest or a browser
// User-Agent, which would claim a human or a browser tab. The contact UA is always present, so
// AO3 still sees exactly who we are. Remove this if AO3 asks.
const fetchSiteNone = "none"

func (t *uaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.Header.Set("User-Agent", t.ua)
	r2.Header.Set("Sec-Fetch-Site", fetchSiteNone)
	return t.next.RoundTrip(r2)
}

// newUATransport wraps next so every request carries ua (AO3 and external clients alike).
func newUATransport(ua string, next http.RoundTripper) (http.RoundTripper, error) {
	if strings.TrimSpace(ua) == "" || !strings.Contains(ua, "contact:") {
		return nil, ErrNoUA
	}
	if next == nil {
		next = defaultTransport()
	}
	return &uaTransport{ua: ua, next: next}, nil
}

func defaultTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		MaxIdleConnsPerHost:   1,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}

func New(o Options) (*Client, error) {
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	base, err := url.Parse(o.BaseURL)
	if err != nil || base.Host == "" {
		return nil, fmt.Errorf("httpx: bad base URL %q", o.BaseURL)
	}
	rt, err := newUATransport(o.UserAgent, o.Transport)
	if err != nil {
		return nil, err
	}
	jar, _ := cookiejar.New(nil)
	// Same cookie AO3's own "Proceed" link sets (works_controller#show: cookies[:view_adult]).
	// It replaces the view_adult=true query param, whose combination with view_full_work=true
	// returned Cloudflare 525s during the Nov 2026 incident while each param alone worked.
	// Secure: the jar still sends it to the plain-http loopback fakeao3 (loopback counts as secure).
	jar.SetCookies(base, []*http.Cookie{{Name: "view_adult", Value: "true", Path: "/",
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}})
	if o.Clock == nil {
		o.Clock = realClock{}
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.MaxBody <= 0 {
		o.MaxBody = defaultMaxBody
	}
	if o.Rand == nil {
		o.Rand = CryptoSource{}
	}
	c := &Client{
		base: base, ua: o.UserAgent, clock: o.Clock, maxBody: o.MaxBody, onReq: o.OnRequest,
		log:   o.Logger.With("component", "httpx"),
		pacer: NewPacer(o.Clock, o.Interval, o.Jitter, o.DailyBudget, o.Rand),
		http: &http.Client{
			Transport: rt,
			Jar:       jar,
			Timeout:   5 * time.Minute, // huge works can be tens of MB
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse // redirects are followed manually, paced and robots-checked
			},
		},
		idPfx: fmt.Sprintf("%04x", o.Rand.Uint64()&0xffff),
	}
	return c, nil
}

// UserAgent returns the UA sent on every request.
func (c *Client) UserAgent() string { return c.ua }

// PacerState exposes backoff/breaker state for `status`.
func (c *Client) PacerState() PacerState { return c.pacer.State() }

// Resolve turns a path like "/works/1" into an absolute URL on the AO3 host.
func (c *Client) Resolve(ref string) (*url.URL, error) {
	r, err := url.Parse(ref)
	if err != nil {
		return nil, err
	}
	u := c.base.ResolveReference(r)
	if u.Host != c.base.Host || u.Scheme != c.base.Scheme {
		return nil, ErrForeign
	}
	return u, nil
}

// Get fetches ref (path or absolute URL on the AO3 host) as the given kind.
// Non-OK classes are returned as a Response with err == nil; callers decide what to do.
// Errors are reserved for: robots disallow, foreign host, context cancel, transport failures,
// and oversized bodies.
func (c *Client) Get(ctx context.Context, kind Kind, ref string) (*Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	u, err := c.Resolve(ref)
	if err != nil {
		return nil, err
	}
	reqID := fmt.Sprintf("%s-%06d", c.idPfx, c.seq.Add(1))
	log := logx.From(ctx, c.log).With("req_id", reqID, "kind", string(kind))
	for hop := 0; ; hop++ {
		if err := c.checkRobots(ctx, log, u); err != nil {
			return nil, err
		}
		resp, err := c.do(ctx, log, reqID, kind, u, hop)
		if err != nil {
			return nil, err
		}
		if resp.Status < 300 || resp.Status >= 400 {
			return resp, nil
		}
		loc, lerr := u.Parse(resp.Header.Get("Location"))
		if lerr != nil {
			resp.Class, resp.Reason = ClassUnexpected, "redirect=bad_location"
			return resp, nil
		}
		if v, ok := classifyRedirect(loc); ok {
			resp.Class, resp.Reason = v.Class, v.Reason
			return resp, nil
		}
		if loc.Host != c.base.Host || hop >= maxRedirects {
			resp.Class, resp.Reason = ClassUnexpected, fmt.Sprintf("redirect=unfollowed foreign=%t hop=%d", loc.Host != c.base.Host, hop)
			return resp, nil
		}
		log.Debug("following redirect", "hop", hop+1, "from", SafePath(u), "to", SafePath(loc))
		u = loc
	}
}

// do performs one paced request and classifies it.
func (c *Client) do(ctx context.Context, log *slog.Logger, reqID string, kind Kind, u *url.URL, hop int) (*Response, error) {
	waited, err := c.pacer.Wait(ctx)
	if err != nil {
		return nil, err
	}
	var t timings
	start := c.clock.Now()
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, t.trace()), http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en")
	safe := SafePath(u)

	hr, err := c.http.Do(req)
	if err != nil && ctx.Err() != nil {
		// We cancelled it (shutdown, Ctrl-C): not an AO3 failure, so no backoff or breaker count.
		st := c.pacer.Finished()
		log.Info("request cancelled", "path", safe, "hop", hop, "wait_ms", waited.Milliseconds(), "consec_fails", st.ConsecFails)
		c.record(RequestRecord{ReqID: reqID, Time: start, Kind: kind, SafePath: safe, Class: ClassUnavailable,
			Reason: "transport:canceled", Duration: c.clock.Now().Sub(start), Waited: waited, Hop: hop, JobID: jobIDFrom(ctx)})
		return nil, ctx.Err()
	}
	if err != nil {
		st := c.pacer.Report(ClassUnavailable, 0)
		log.Warn("request failed", "path", safe, "hop", hop, "err", errClass(err), "wait_ms", waited.Milliseconds(),
			"consec_fails", st.ConsecFails, "backoff_until", st.BackoffTill, "breaker_open", st.BreakerOpen)
		c.record(RequestRecord{ReqID: reqID, Time: start, Kind: kind, SafePath: safe, Class: ClassUnavailable,
			Reason: "transport:" + errClass(err), Duration: c.clock.Now().Sub(start), Waited: waited, Hop: hop, JobID: jobIDFrom(ctx)})
		return &Response{ReqID: reqID, Kind: kind, URL: u, Class: ClassUnavailable, Reason: "transport:" + errClass(err), Header: http.Header{}}, nil
	}
	var body []byte
	var rerr error
	if hr.ContentLength > c.maxBody {
		rerr = ErrTooLarge // declared too large: don't download it
	} else {
		body, rerr = io.ReadAll(io.LimitReader(hr.Body, c.maxBody+1))
		if rerr == nil && int64(len(body)) > c.maxBody {
			rerr = ErrTooLarge
		}
	}
	hr.Body.Close()
	total := c.clock.Now().Sub(start)
	if rerr != nil && ctx.Err() != nil {
		rerr = ctx.Err() // cancelled mid-body
	}

	var v Verdict
	tooLarge := errors.Is(rerr, ErrTooLarge)
	cancelled := rerr != nil && ctx.Err() != nil
	switch {
	case tooLarge && hr.StatusCode >= 400:
		v = classify(kind, hr.StatusCode, nil) // an error page is still an error (429/5xx back off)
	case tooLarge:
		v = Verdict{ClassUnexpected, "too_large"}
	case cancelled:
		v = Verdict{ClassUnavailable, "read:canceled"}
	case rerr != nil:
		v = Verdict{ClassUnavailable, "read:" + errClass(rerr)}
	case hr.StatusCode >= 300 && hr.StatusCode < 400:
		v = Verdict{ClassOK, fmt.Sprintf("status=%d redirect", hr.StatusCode)} // resolved by caller
	default:
		v = classify(kind, hr.StatusCode, body)
	}
	retryAfter := parseRetryAfter(hr.Header.Get("Retry-After"), c.clock.Now())
	var st PacerState
	if (tooLarge && hr.StatusCode < 400) || cancelled {
		// AO3 answered fine (we refused the size) or we gave up: neither is AO3 failing.
		st = c.pacer.Finished()
	} else {
		st = c.pacer.Report(v.Class, retryAfter)
	}

	level := slog.LevelInfo
	switch v.Class {
	case ClassRateLimited, ClassUnavailable, ClassUnexpected:
		level = slog.LevelWarn
	}
	log.Log(ctx, level, "http",
		"path", safe, "hop", hop, "status", hr.StatusCode, "class", string(v.Class), "reason", v.Reason,
		"bytes", len(body), "content_type", hr.Header.Get("Content-Type"),
		"retry_after", hr.Header.Get("Retry-After"), "cf_ray", hr.Header.Get("Cf-Ray"),
		"cf_cache_status", hr.Header.Get("Cf-Cache-Status"), "proto", hr.Proto,
		"wait_ms", waited.Milliseconds(), "dns_ms", t.ms(t.dnsStart, t.dnsDone),
		"connect_ms", t.ms(t.connStart, t.connDone), "tls_ms", t.ms(t.tlsStart, t.tlsDone),
		"ttfb_ms", t.ms(t.wroteReq, t.firstByte), "total_ms", total.Milliseconds(), "conn_reused", t.reused,
		"interval_ms", st.Interval.Milliseconds(), "slow_factor", st.Factor, "consec_fails", st.ConsecFails,
		"backoff_until", st.BackoffTill, "breaker_open", st.BreakerOpen, "day_count", st.DayCount,
	)
	c.record(RequestRecord{ReqID: reqID, Time: start, Kind: kind, SafePath: safe, Status: hr.StatusCode,
		Class: v.Class, Reason: v.Reason, Bytes: len(body), Duration: total, Waited: waited, Hop: hop, JobID: jobIDFrom(ctx)})
	if cancelled {
		return nil, ctx.Err()
	}
	if tooLarge && hr.StatusCode < 400 {
		return nil, fmt.Errorf("%w (%d bytes) %s", ErrTooLarge, c.maxBody, safe)
	}
	if tooLarge {
		body = nil // classified by status alone
	}
	return &Response{ReqID: reqID, Kind: kind, URL: u, Status: hr.StatusCode, Header: hr.Header, Body: body,
		Class: v.Class, Reason: v.Reason}, nil
}

func (c *Client) record(r RequestRecord) {
	if c.onReq != nil {
		c.onReq(r)
	}
}

// checkRobots refreshes robots.txt when stale (one paced request) and tests u against it.
func (c *Client) checkRobots(ctx context.Context, log *slog.Logger, u *url.URL) error {
	now := c.clock.Now()
	if !c.robots.Fresh(now) {
		ru := c.base.ResolveReference(&url.URL{Path: "/robots.txt"})
		resp, err := c.do(ctx, log, fmt.Sprintf("%s-r%06d", c.idPfx, c.seq.Add(1)), KindRobots, ru, 0)
		if err != nil {
			return err
		}
		status := resp.Status
		if status >= 300 && status < 400 {
			status = 500 // redirected robots: be conservative
		}
		rc, perr := NewRobots(status, resp.Body, c.clock.Now(), resp.Class == ClassUnavailable)
		if perr != nil {
			log.Warn("robots parse failed; disallowing all", "err", perr.Error())
			rc, _ = NewRobots(500, nil, c.clock.Now(), true)
		}
		c.robots = rc
		log.Info("robots refreshed", "status", resp.Status, "effective_status", rc.status, "ttl", rc.ttl.String())
	}
	if !c.robots.Allowed(u) {
		log.Warn("robots disallowed; not sending", "path", SafePath(u), "robots_status", c.robots.status)
		return fmt.Errorf("%w: %s", ErrDisallowed, SafePath(u))
	}
	log.Debug("robots allowed", "path", SafePath(u))
	return nil
}

// errClass reduces an error to a content-free label.
func errClass(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, ErrTooLarge):
		return "too_large"
	}
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	s := err.Error()
	for _, k := range []string{"connection refused", "connection reset", "no such host", "tls", "EOF", "certificate"} {
		if strings.Contains(s, k) {
			return strings.ReplaceAll(k, " ", "_")
		}
	}
	return "other"
}

type timings struct {
	dnsStart, dnsDone, connStart, connDone, tlsStart, tlsDone, wroteReq, firstByte time.Time
	reused                                                                         bool
}

func (t *timings) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart:             func(httptrace.DNSStartInfo) { t.dnsStart = time.Now() },
		DNSDone:              func(httptrace.DNSDoneInfo) { t.dnsDone = time.Now() },
		ConnectStart:         func(string, string) { t.connStart = time.Now() },
		ConnectDone:          func(string, string, error) { t.connDone = time.Now() },
		TLSHandshakeStart:    func() { t.tlsStart = time.Now() },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { t.tlsDone = time.Now() },
		GotConn:              func(i httptrace.GotConnInfo) { t.reused = i.Reused },
		WroteRequest:         func(httptrace.WroteRequestInfo) { t.wroteReq = time.Now() },
		GotFirstResponseByte: func() { t.firstByte = time.Now() },
	}
}

func (t *timings) ms(a, b time.Time) int64 {
	if a.IsZero() || b.IsZero() {
		return -1
	}
	return b.Sub(a).Milliseconds()
}
