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
	Seed        uint64              // jitter RNG seed; 0 = time-based
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
}

// Response is a fully read AO3 response.
type Response struct {
	ReqID     string
	Kind      Kind
	URL       *url.URL // final URL after followed redirects
	Status    int
	Header    http.Header
	Body      []byte
	Class     Class
	Reason    string
	Redirects []string // SafePath of each hop
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
	robots *robotsCache
	seq    atomic.Uint64
	idPfx  string
}

// uaTransport forces the User-Agent on every outgoing request, whatever the caller set.
type uaTransport struct {
	ua   string
	next http.RoundTripper
}

func (t *uaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.Header.Set("User-Agent", t.ua)
	return t.next.RoundTrip(r2)
}

// NewUATransport wraps next so every request carries ua. Image fetchers use this too.
func NewUATransport(ua string, next http.RoundTripper) (http.RoundTripper, error) {
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
	rt, err := NewUATransport(o.UserAgent, o.Transport)
	if err != nil {
		return nil, err
	}
	jar, _ := cookiejar.New(nil)
	if o.Clock == nil {
		o.Clock = realClock{}
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.MaxBody <= 0 {
		o.MaxBody = defaultMaxBody
	}
	if o.Seed == 0 {
		o.Seed = uint64(time.Now().UnixNano())
	}
	c := &Client{
		base: base, ua: o.UserAgent, clock: o.Clock, maxBody: o.MaxBody, onReq: o.OnRequest,
		log:   o.Logger.With("component", "httpx"),
		pacer: NewPacer(o.Clock, o.Interval, o.Jitter, o.DailyBudget, o.Seed),
		http: &http.Client{
			Transport: rt,
			Jar:       jar,
			Timeout:   5 * time.Minute, // huge works can be tens of MB
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse // redirects are followed manually, paced and robots-checked
			},
		},
		idPfx: fmt.Sprintf("%04x", o.Seed&0xffff),
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
	var hops []string
	for hop := 0; ; hop++ {
		if err := c.checkRobots(ctx, log, u); err != nil {
			return nil, err
		}
		resp, err := c.do(ctx, log, reqID, kind, u, hop)
		if err != nil {
			return nil, err
		}
		if resp.Status >= 300 && resp.Status < 400 {
			loc, lerr := u.Parse(resp.Header.Get("Location"))
			if lerr != nil {
				resp.Class, resp.Reason = ClassUnexpected, "redirect=bad_location"
				return c.finish(log, resp, hops), nil
			}
			if v, ok := classifyRedirect(loc); ok {
				resp.Class, resp.Reason = v.Class, v.Reason
				return c.finish(log, resp, hops), nil
			}
			if loc.Host != c.base.Host || hop >= maxRedirects {
				resp.Class, resp.Reason = ClassUnexpected, fmt.Sprintf("redirect=unfollowed foreign=%t hop=%d", loc.Host != c.base.Host, hop)
				return c.finish(log, resp, hops), nil
			}
			hops = append(hops, SafePath(loc))
			log.Debug("following redirect", "hop", hop+1, "from", SafePath(u), "to", SafePath(loc))
			u = loc
			continue
		}
		return c.finish(log, resp, hops), nil
	}
}

func (c *Client) finish(log *slog.Logger, r *Response, hops []string) *Response {
	r.Redirects = hops
	return r
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
	if err != nil {
		st := c.pacer.Report(ClassUnavailable, 0)
		log.Warn("request failed", "path", safe, "hop", hop, "err", errClass(err), "wait_ms", waited.Milliseconds(),
			"consec_fails", st.ConsecFails, "backoff_until", st.BackoffTill, "breaker_open", st.BreakerOpen)
		c.record(RequestRecord{ReqID: reqID, Time: start, Kind: kind, SafePath: safe, Class: ClassUnavailable,
			Reason: "transport:" + errClass(err), Duration: c.clock.Now().Sub(start), Waited: waited, Hop: hop})
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return &Response{ReqID: reqID, Kind: kind, URL: u, Class: ClassUnavailable, Reason: "transport:" + errClass(err), Header: http.Header{}}, nil
	}
	body, rerr := io.ReadAll(io.LimitReader(hr.Body, c.maxBody+1))
	hr.Body.Close()
	total := c.clock.Now().Sub(start)
	if rerr == nil && int64(len(body)) > c.maxBody {
		rerr = ErrTooLarge
	}

	var v Verdict
	switch {
	case rerr != nil:
		v = Verdict{ClassUnavailable, "read:" + errClass(rerr)}
	case hr.StatusCode >= 300 && hr.StatusCode < 400:
		v = Verdict{ClassOK, fmt.Sprintf("status=%d redirect", hr.StatusCode)} // resolved by caller
	default:
		v = classify(kind, hr.StatusCode, hr.Header, body)
	}
	retryAfter := parseRetryAfter(hr.Header.Get("Retry-After"), c.clock.Now())
	st := c.pacer.Report(v.Class, retryAfter)

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
		Class: v.Class, Reason: v.Reason, Bytes: len(body), Duration: total, Waited: waited, Hop: hop})
	if rerr != nil && errors.Is(rerr, ErrTooLarge) {
		return nil, fmt.Errorf("%w (%d bytes) %s", ErrTooLarge, c.maxBody, safe)
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
	if !c.robots.fresh(now) {
		ru := c.base.ResolveReference(&url.URL{Path: "/robots.txt"})
		resp, err := c.do(ctx, log, fmt.Sprintf("%s-r%06d", c.idPfx, c.seq.Add(1)), KindRobots, ru, 0)
		if err != nil {
			return err
		}
		status := resp.Status
		if status >= 300 && status < 400 {
			status = 500 // redirected robots: be conservative
		}
		rc, perr := newRobots(status, resp.Body, c.clock.Now(), resp.Class == ClassUnavailable)
		if perr != nil {
			log.Warn("robots parse failed; disallowing all", "err", perr.Error())
			rc, _ = newRobots(500, nil, c.clock.Now(), true)
		}
		c.robots = rc
		log.Info("robots refreshed", "status", resp.Status, "effective_status", rc.status, "ttl", rc.ttl.String())
	}
	target := u.EscapedPath()
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	if !c.robots.allowed(target) {
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
