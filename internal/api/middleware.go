package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"slices"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
)

const (
	// maxURLBytes caps the escaped path plus the raw query (414 above it).
	maxURLBytes = 4 << 10
	// maxClientReqIDBytes caps a client-sent X-Request-Id we keep.
	maxClientReqIDBytes = 64
	// Throttle: requests handled at once, requests allowed to wait, and how long they wait.
	maxInFlight = 64
	maxBacklog  = 256
	backlogWait = 5 * time.Second
)

const (
	headerRequestID       = "X-Request-ID"
	headerClientRequestID = "X-Client-Request-Id"
	headerFunnel          = "Tailscale-Funnel-Request"
)

// reqState is the per-request state the middleware share. requestID puts it in the context;
// reveal sets reveal, which the enforcer and access log read after the handler returns.
type reqState struct {
	id              string
	clientID        string
	clientIDInvalid bool
	reveal          bool
}

type stateKey struct{}

// state returns the request's state. Outside the middleware chain (unit tests of handlers) it
// returns a fresh, hashed-mode state.
func state(ctx context.Context) *reqState {
	if st, ok := ctx.Value(stateKey{}).(*reqState); ok {
		return st
	}
	return &reqState{}
}

// requestID gives each request a random server ID (X-Request-Id) and keeps a well-formed
// client-sent X-Request-Id as client_req_id (echoed as X-Client-Request-Id). It never trusts
// the client's ID as its own: see docs/part2-api.md for why chi's RequestID isn't used.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b [8]byte
		_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error
		st := &reqState{id: hex.EncodeToString(b[:])}
		if vals := r.Header.Values(headerRequestID); len(vals) > 0 {
			if len(vals) == 1 && validClientReqID(vals[0]) {
				st.clientID = vals[0]
				w.Header().Set(headerClientRequestID, st.clientID)
			} else {
				st.clientIDInvalid = true
			}
		}
		w.Header().Set(headerRequestID, st.id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), stateKey{}, st)))
	})
}

// validClientReqID accepts 1..64 bytes of [A-Za-z0-9._:-].
func validClientReqID(s string) bool {
	if s == "" || len(s) > maxClientReqIDBytes {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '.' || c == '_' || c == ':' || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

// statusWriter records the status and body size for the access log.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// loggedMethods are logged by name; any other (client-chosen) method is logged as "other".
var loggedMethods = append([]string{http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace}, routedMethods...)

// accessLog logs one line per request. It logs the route pattern and numeric path IDs, never
// the raw path, query values, bodies or response strings. Query parameter names are logged
// only when they are names the API declares (dto.ParamName); others are only counted, since a
// client chooses them freely.
func (a *API) accessLog(listener string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)

			st := state(r.Context())
			method := "other"
			if slices.Contains(loggedMethods, r.Method) {
				method = r.Method
			}
			route, rctx, prerouted := loggedRoute(r)
			attrs := make([]slog.Attr, 0, 16)
			attrs = append(attrs,
				slog.String("req_id", st.id),
				slog.String("listener", listener),
				slog.String("method", method),
				slog.String("route", route),
				slog.Int("status", sw.status),
				slog.Int("bytes", sw.bytes),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("client_ip", middleware.GetClientIP(r.Context())),
				slog.Bool("funnel", r.Header.Get(headerFunnel) == "?1"),
				slog.Bool("reveal", st.reveal),
			)
			if st.clientID != "" {
				attrs = append(attrs, slog.String("client_req_id", st.clientID))
			}
			if st.clientIDInvalid {
				attrs = append(attrs, slog.Bool("client_req_id_invalid", true))
			}
			if prerouted {
				attrs = append(attrs, slog.Bool("prerouted", true))
			}
			attrs = append(attrs, pathIDAttrs(rctx)...)
			attrs = append(attrs, queryNameAttrs(r.URL.RawQuery)...)
			a.log.LogAttrs(r.Context(), slog.LevelInfo, "api request", attrs...)
		})
	}
}

// loggedRoute returns the route pattern to log and the route context holding its path params.
// A request that middleware rejected before routing (rate limit, throttle, URL limits,
// cross-origin) has no pattern yet, so it is resolved here without running the handler and
// prerouted is true. A path that matches no route stays "": the raw path may hold names.
func loggedRoute(r *http.Request) (route string, rctx *chi.Context, prerouted bool) {
	rctx = chi.RouteContext(r.Context())
	if rctx == nil {
		return "", nil, false
	}
	if p := rctx.RoutePattern(); p != "" || rctx.Routes == nil {
		return p, rctx, false
	}
	path := r.URL.RawPath // the path chi itself routes on
	if path == "" {
		path = r.URL.Path
	}
	found := chi.NewRouteContext()
	if p := rctx.Routes.Find(found, r.Method, path); p != "" {
		return p, found, true
	}
	return "", rctx, false
}

// routePattern is the matched chi route pattern, or "" when nothing matched.
func routePattern(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		return rctx.RoutePattern()
	}
	return ""
}

// pathIDAttrs logs numeric path parameters as path_<name>; a non-numeric value is logged only
// as path_<name>_invalid. The names come from our route patterns.
func pathIDAttrs(rctx *chi.Context) []slog.Attr {
	if rctx == nil {
		return nil
	}
	var attrs []slog.Attr
	for i, k := range rctx.URLParams.Keys {
		if k == "*" {
			continue
		}
		if id, err := strconv.ParseInt(rctx.URLParams.Values[i], 10, 64); err == nil && id > 0 {
			attrs = append(attrs, slog.Int64("path_"+k, id))
		} else {
			attrs = append(attrs, slog.Bool("path_"+k+"_invalid", true))
		}
	}
	return attrs
}

// queryNameAttrs logs the declared parameter names present in the query and counts the rest.
func queryNameAttrs(rawQuery string) []slog.Attr {
	if rawQuery == "" {
		return nil
	}
	vals, err := url.ParseQuery(rawQuery)
	if err != nil {
		return []slog.Attr{slog.Bool("query_invalid", true)}
	}
	var names []string
	other := 0
	for name := range vals {
		if dto.ParamName(name).Known() {
			names = append(names, name)
		} else {
			other++
		}
	}
	slices.Sort(names)
	attrs := []slog.Attr{slog.Any("params", names)}
	if other > 0 {
		attrs = append(attrs, slog.Int("other_params", other))
	}
	return attrs
}

// recoverer turns a handler panic into a 500 problem. It logs the panic's type and the stack,
// never the panic value (which could hold content). http.ErrAbortHandler is re-panicked so the
// server aborts the response as intended.
func (a *API) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(v)
			}
			a.log.LogAttrs(r.Context(), slog.LevelError, "api panic",
				slog.String("req_id", state(r.Context()).id),
				slog.String("route", routePattern(r)),
				slog.String("panic_type", fmt.Sprintf("%T", v)),
				slog.String("stack", string(debug.Stack())))
			w.Header().Set(headerFanwork, "hashed")
			p := errCode(dto.CodeInternal).problem()
			respond(w, r, a.log, p.Status, contentTypeProblem, p)
		}()
		next.ServeHTTP(w, r)
	})
}

// securityHeaders sets the headers every response carries. There are no CORS headers: the UI is
// same-origin.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// urlLimits rejects URLs whose escaped path plus raw query exceed maxURLBytes (414). Parameter
// counts, repeats and names are checked per route by parseQuery.
func (a *API) urlLimits(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.EscapedPath())+len(r.URL.RawQuery) > maxURLBytes {
			respondError(w, r, a.log, errCode(dto.CodeURITooLong))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimit limits requests per client per window. The key is the trusted client IP (IPv6 by
// /64); requests without one (a local client on the public listener, or the admin socket) share
// the listener's fixed bucket, so a missing IP never bypasses the limit. httprate sets
// Retry-After on a 429.
func (a *API) rateLimit(n int, window time.Duration, listener string) func(http.Handler) http.Handler {
	key := func(r *http.Request) (string, error) {
		if ip := middleware.GetClientIP(r.Context()); ip != "" {
			return httprate.CanonicalizeIP(ip), nil
		}
		return listener, nil
	}
	return httprate.LimitBy(n, window, key,
		httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
			respondError(w, r, a.log, errCode(dto.CodeRateLimited))
		}),
		httprate.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
			respondError(w, r, a.log, fmt.Errorf("rate limiter: %w", err))
		}),
	)
}

// throttle caps the requests handled at once at inFlight, lets up to backlog more wait up to
// wait for a slot, and rejects the rest with 503 + Retry-After. It is chi's ThrottleBacklog,
// rewritten so rejections are problems (chi's writes text/plain, which the enforcer would
// turn into a misleading reveal_required).
func (a *API) throttle(inFlight, backlog int, wait time.Duration) func(http.Handler) http.Handler {
	tokens := make(chan struct{}, inFlight)
	queue := make(chan struct{}, inFlight+backlog)
	reject := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		respondError(w, r, a.log, errCode(dto.CodeUnavailable))
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case queue <- struct{}{}:
				defer func() { <-queue }()
			default:
				reject(w, r)
				return
			}
			t := time.NewTimer(wait)
			defer t.Stop()
			select {
			case tokens <- struct{}{}:
				defer func() { <-tokens }()
			case <-t.C:
				reject(w, r)
				return
			case <-r.Context().Done():
				reject(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// timeout gives each request a context deadline. Handlers and SQLite queries honour it; a
// deadline error maps to 503 (problemFor).
func timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// crossOrigin rejects cross-origin browser requests with unsafe methods (Go's
// CrossOriginProtection, Sec-Fetch-Site/Origin based) with a 403 problem.
func (a *API) crossOrigin() func(http.Handler) http.Handler {
	c := http.NewCrossOriginProtection()
	c.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respondError(w, r, a.log, errCode(dto.CodeForbidden))
	}))
	return c.Handler
}

// reveal parses the reveal param once per request. Only the exact value "1", once, is
// accepted; with NoReveal it is refused (403). On success the enforcer runs in revealed mode.
func (a *API) reveal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vals, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			respondError(w, r, a.log, errCode(dto.CodeBadRequest))
			return
		}
		vs, ok := vals[RevealParam]
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		switch {
		case len(vs) != 1:
			respondError(w, r, a.log, errField(dto.CodeDuplicateParam, RevealParam))
		case vs[0] != "1":
			respondError(w, r, a.log, errField(dto.CodeInvalidParam, RevealParam))
		case a.opts.NoReveal:
			respondError(w, r, a.log, errCode(dto.CodeRevealDisabled))
		default:
			state(r.Context()).reveal = true
			next.ServeHTTP(w, r)
		}
	})
}
