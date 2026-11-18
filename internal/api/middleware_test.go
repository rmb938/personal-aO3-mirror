package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
)

func TestRequestID(t *testing.T) {
	a, logs := newTestAPI(t, nil)
	w := get(a.Public(), "/api/v1/healthz")
	id := w.Header().Get(headerRequestID)
	assert.Regexp(t, `^[0-9a-f]{16}$`, id)
	assert.Empty(t, w.Header().Get(headerClientRequestID))
	assert.Equal(t, id, logs.accessLine(t)["req_id"])

	w2 := get(a.Public(), "/api/v1/healthz")
	assert.NotEqual(t, id, w2.Header().Get(headerRequestID), "a fresh ID per request")
}

func TestClientRequestID(t *testing.T) {
	cases := []struct {
		name  string
		vals  []string
		valid bool
	}{
		{"valid", []string{"abc.DEF_123:x-y"}, true},
		{"max length", []string{strings.Repeat("a", maxClientReqIDBytes)}, true},
		{"too long", []string{strings.Repeat("a", maxClientReqIDBytes+1)}, false},
		{"bad chars", []string{"a b"}, false},
		{"slash", []string{"a/b"}, false},
		{"empty", []string{""}, false},
		{"repeated", []string{"a", "b"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, logs := newTestAPI(t, nil)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
			for _, v := range c.vals {
				req.Header.Add(headerRequestID, v)
			}
			w := do(a.Public(), req)
			require.Equal(t, http.StatusOK, w.Code)
			assert.Regexp(t, `^[0-9a-f]{16}$`, w.Header().Get(headerRequestID),
				"the server ID is always our own")
			line := logs.accessLine(t)
			if c.valid {
				assert.Equal(t, c.vals[0], w.Header().Get(headerClientRequestID))
				assert.Equal(t, c.vals[0], line["client_req_id"])
				assert.NotContains(t, line, "client_req_id_invalid")
			} else {
				assert.Empty(t, w.Header().Get(headerClientRequestID))
				assert.NotContains(t, line, "client_req_id")
				assert.Equal(t, true, line["client_req_id_invalid"])
			}
		})
	}
}

func TestClientIPTrust(t *testing.T) {
	cases := []struct {
		name   string
		remote string
		xff    []string
		want   string
	}{
		{"loopback proxy", "127.0.0.1:5000", []string{"203.0.113.7"}, "203.0.113.7"},
		{"loopback v6 proxy", "[::1]:5000", []string{"203.0.113.7"}, "203.0.113.7"},
		{"trusted hops skipped", "127.0.0.1:5000", []string{"198.51.100.9, 203.0.113.7, 127.0.0.1"}, "203.0.113.7"},
		{"direct client ignores XFF", "198.51.100.2:5000", []string{"203.0.113.7"}, "198.51.100.2"},
		{"loopback without XFF", "127.0.0.1:5000", nil, ""},
		{"garbage fails closed", "127.0.0.1:5000", []string{"not-an-ip"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, logs := newTestAPI(t, nil)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
			req.RemoteAddr = c.remote
			if c.xff != nil {
				req.Header["X-Forwarded-For"] = c.xff
			}
			require.Equal(t, http.StatusOK, do(a.Public(), req).Code)
			assert.Equal(t, c.want, logs.accessLine(t)["client_ip"])
		})
	}

	t.Run("admin socket never trusts XFF", func(t *testing.T) {
		a, logs := newTestAPI(t, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
		req.RemoteAddr = "@"
		req.Header.Set("X-Forwarded-For", "203.0.113.7")
		require.Equal(t, http.StatusOK, do(a.Admin(), req).Code)
		assert.Empty(t, logs.accessLine(t)["client_ip"])
	})
}

func TestAccessLogIsContentFree(t *testing.T) {
	a, logs := newTestAPI(t, nil)
	a.public.Get("/items/{id}/parts/{part}", a.handle(func(*http.Request) (any, error) { return nil, errCode(dto.CodeNotFound) }))

	req := httptest.NewRequest(http.MethodGet, "/items/42/parts/"+sentinel+"part?page="+sentinel+"v&"+sentinel+"n=1", nil)
	req.Header.Set(headerFunnel, "?1")
	do(a.public, req)
	line := logs.accessLine(t)
	assert.Equal(t, "/items/{id}/parts/{part}", line["route"])
	assert.Equal(t, "public", line["listener"])
	assert.Equal(t, http.MethodGet, line["method"])
	assert.InDelta(t, 42, line["path_id"], 0)
	assert.Equal(t, true, line["path_part_invalid"])
	assert.Equal(t, []any{"page"}, line["params"])
	assert.InDelta(t, 1, line["other_params"], 0)
	assert.Equal(t, true, line["funnel"])
	assert.Equal(t, false, line["reveal"])
	assert.InDelta(t, http.StatusNotFound, line["status"], 0)
	assert.NotContains(t, logs.String(), sentinel)
}

// A request rejected before routing (here, rate limited) still logs its route pattern and
// numeric path IDs, flagged prerouted. An unmatched path logs no route.
func TestAccessLogPreroutedRoute(t *testing.T) {
	a, logs := newTestAPI(t, func(o *Options) { o.GeneralPerMin = 1 })
	a.public.Get("/items/{id}/parts/{part}", a.handle(func(*http.Request) (any, error) { return nil, nil }))

	require.Equal(t, http.StatusOK, get(a.public, "/items/1/parts/2").Code)
	for _, target := range []string{"/items/42/parts/" + sentinel + "part", "/" + sentinel + "nope"} {
		require.Equal(t, http.StatusTooManyRequests, get(a.public, target).Code, target)
	}
	var lines []map[string]any
	for _, l := range logs.lines(t) {
		if l["msg"] == "api request" {
			lines = append(lines, l)
		}
	}
	require.Len(t, lines, 3)
	assert.Nil(t, lines[0]["prerouted"], "routed request")

	limited := lines[1]
	assert.Equal(t, "/items/{id}/parts/{part}", limited["route"])
	assert.Equal(t, true, limited["prerouted"])
	assert.InDelta(t, 42, limited["path_id"], 0)
	assert.Equal(t, true, limited["path_part_invalid"])

	assert.Empty(t, lines[2]["route"], "unmatched path")
	assert.Nil(t, lines[2]["prerouted"])
	assert.NotContains(t, logs.String(), sentinel)
}

func TestAccessLogClientMethod(t *testing.T) {
	a, logs := newTestAPI(t, nil)
	w := do(a.Public(), httptest.NewRequest("SENTINELMETHOD", "/api/v1/healthz", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Equal(t, "other", logs.accessLine(t)["method"])
	assert.NotContains(t, logs.String(), "SENTINEL")
}

func TestRecoverer(t *testing.T) {
	a, logs := newTestAPI(t, nil)
	a.public.Get("/panic", func(http.ResponseWriter, *http.Request) { panic(sentinel + "panic value") })
	w := get(a.public, "/panic")
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, dto.CodeInternal, decodeProblem(t, w).Code)
	assert.Equal(t, "hashed", w.Header().Get(headerFanwork))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"), "security headers survive a panic")
	assert.Contains(t, logs.String(), `"panic_type":"string"`)
	assert.Contains(t, logs.String(), "stack")
	assert.NotContains(t, logs.String(), sentinel)

	a.public.Get("/abort", func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	assert.PanicsWithValue(t, http.ErrAbortHandler, func() { get(a.public, "/abort") })
}

func TestSecurityHeaders(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	for _, target := range []string{"/api/v1/healthz", "/api/v1/nope"} {
		h := get(a.Public(), target).Header()
		assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"))
		assert.Equal(t, "default-src 'none'; frame-ancestors 'none'", h.Get("Content-Security-Policy"))
		assert.Equal(t, "no-referrer", h.Get("Referrer-Policy"))
		assert.Equal(t, "same-origin", h.Get("Cross-Origin-Resource-Policy"))
		assert.Empty(t, h.Get("Access-Control-Allow-Origin"))
	}
}

func TestURLLimits(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	long := "/api/v1/healthz?x=" + strings.Repeat("a", maxURLBytes)
	w := get(a.Public(), long)
	require.Equal(t, http.StatusRequestURITooLong, w.Code)
	assert.Equal(t, dto.CodeURITooLong, decodeProblem(t, w).Code)

	w = get(a.Public(), "/"+strings.Repeat("a", maxURLBytes))
	assert.Equal(t, http.StatusRequestURITooLong, w.Code)
}

func TestRateLimit(t *testing.T) {
	a, _ := newTestAPI(t, func(o *Options) { o.GeneralPerMin = 2 })
	fromIP := func(ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
		req.RemoteAddr = ip + ":1234"
		return do(a.Public(), req)
	}
	assert.Equal(t, http.StatusOK, fromIP("198.51.100.1").Code)
	assert.Equal(t, http.StatusOK, fromIP("198.51.100.1").Code)
	w := fromIP("198.51.100.1")
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, dto.CodeRateLimited, decodeProblem(t, w).Code)
	assert.Equal(t, "60", w.Header().Get("Retry-After"))
	assert.Equal(t, "hashed", w.Header().Get(headerFanwork))

	assert.Equal(t, http.StatusOK, fromIP("198.51.100.2").Code, "another client has its own bucket")

	// IPv6 clients are bucketed by /64.
	assert.Equal(t, http.StatusOK, fromIP("[2001:db8::1]").Code)
	assert.Equal(t, http.StatusOK, fromIP("[2001:db8::2]").Code)
	assert.Equal(t, http.StatusTooManyRequests, fromIP("[2001:db8::3]").Code)

	// Requests with no client IP (local, via a trusted proxy without XFF) share one bucket.
	assert.Equal(t, http.StatusOK, fromIP("127.0.0.1").Code)
	assert.Equal(t, http.StatusOK, fromIP("[::1]").Code)
	assert.Equal(t, http.StatusTooManyRequests, fromIP("127.0.0.1").Code)

	// The admin socket has its own, higher limit.
	for range 2 * adminRateFactor {
		require.Equal(t, http.StatusOK, get(a.Admin(), "/api/v1/healthz").Code)
	}
	assert.Equal(t, http.StatusTooManyRequests, get(a.Admin(), "/api/v1/healthz").Code)
}

// blockingThrottle wraps a handler that blocks until release is closed, signalling started.
func blockingThrottle(t *testing.T, backlog int, wait time.Duration) (h http.Handler, started chan struct{}, release chan struct{}) {
	t.Helper()
	a, _ := newTestAPI(t, nil)
	started, release = make(chan struct{}, 4), make(chan struct{})
	h = a.throttle(1, backlog, wait)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	return h, started, release
}

func TestThrottleBacklogWaitTimesOut(t *testing.T) {
	h, started, release := blockingThrottle(t, 1, 20*time.Millisecond)
	var wg sync.WaitGroup
	first := httptest.NewRecorder()
	wg.Go(func() { h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil)) })
	<-started

	// The next waits in the backlog and times out while the first holds the only slot.
	w := do(h, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, dto.CodeUnavailable, decodeProblem(t, w).Code)
	assert.Equal(t, "1", w.Header().Get("Retry-After"))

	close(release)
	wg.Wait()
	assert.Equal(t, http.StatusNoContent, first.Code)
}

func TestThrottleBacklogFullRejectsAtOnce(t *testing.T) {
	h, started, release := blockingThrottle(t, 0, time.Hour)
	var wg sync.WaitGroup
	wg.Go(func() { h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil)) })
	<-started

	// No backlog room: rejected without waiting (the wait is an hour).
	w := do(h, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	close(release)
	wg.Wait()
}

func TestThrottleBackloggedRequestRuns(t *testing.T) {
	h, started, release := blockingThrottle(t, 1, time.Hour)
	var wg sync.WaitGroup
	first, second := httptest.NewRecorder(), httptest.NewRecorder()
	wg.Go(func() { h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil)) })
	<-started
	wg.Go(func() { h.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/", nil)) })
	close(release)
	wg.Wait()
	assert.Equal(t, http.StatusNoContent, first.Code)
	assert.Equal(t, http.StatusNoContent, second.Code, "the waiting request runs once the slot frees")
}

func TestThrottleCancelledWhileWaiting(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	release := make(chan struct{})
	started := make(chan struct{})
	h := a.throttle(1, 1, time.Minute)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
	}))
	var wg sync.WaitGroup
	wg.Go(func() { h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil)) })
	<-started

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	w := do(h, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	close(release)
	wg.Wait()
}

func TestRequestTimeout(t *testing.T) {
	a, _ := newTestAPI(t, func(o *Options) { o.RequestTimeout = 20 * time.Millisecond })
	a.public.Get("/slow", a.handle(func(r *http.Request) (any, error) {
		_, ok := r.Context().Deadline()
		assert.True(t, ok)
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	w := get(a.public, "/slow")
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, dto.CodeUnavailable, decodeProblem(t, w).Code)
}

func TestCrossOrigin(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/healthz", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := do(a.Public(), req)
	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, dto.CodeForbidden, decodeProblem(t, w).Code)

	req = httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	assert.Equal(t, http.StatusOK, do(a.Public(), req).Code, "safe methods are allowed")

	req = httptest.NewRequest(http.MethodPost, "/api/v1/healthz", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	assert.Equal(t, http.StatusMethodNotAllowed, do(a.Public(), req).Code)
}

func TestReveal(t *testing.T) {
	a, logs := newTestAPI(t, nil)
	w := get(a.Public(), "/api/v1/healthz"+revealQuery)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "revealed", w.Header().Get(headerFanwork))
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	assert.Empty(t, w.Header().Get(headerFanworkEpoch))
	assert.Equal(t, true, logs.accessLine(t)["reveal"])

	w = get(a.Public(), "/api/v1/healthz")
	assert.Equal(t, "hashed", w.Header().Get(headerFanwork))
	assert.Empty(t, w.Header().Get("Cache-Control"))
	assert.NotEmpty(t, w.Header().Get(headerFanworkEpoch))

	bad := map[string]struct {
		query string
		code  dto.ProblemCode
	}{
		"other value": {"?" + RevealParam + "=true", dto.CodeInvalidParam},
		"empty value": {"?" + RevealParam + "=", dto.CodeInvalidParam},
		"repeated":    {revealQuery + "&" + RevealParam + "=1", dto.CodeDuplicateParam},
		"bad query":   {"?%zz", dto.CodeBadRequest},
	}
	for name, c := range bad {
		t.Run(name, func(t *testing.T) {
			w := get(a.Public(), "/api/v1/healthz"+c.query)
			require.Equal(t, http.StatusBadRequest, w.Code)
			p := decodeProblem(t, w)
			assert.Equal(t, c.code, p.Code)
			assert.Equal(t, "hashed", w.Header().Get(headerFanwork))
		})
	}
}

func TestNoRevealRefusesReveal(t *testing.T) {
	a, _ := newTestAPI(t, func(o *Options) { o.NoReveal = true })
	for _, h := range []http.Handler{a.Public(), a.Admin()} {
		w := get(h, "/api/v1/healthz"+revealQuery)
		require.Equal(t, http.StatusForbidden, w.Code)
		assert.Equal(t, dto.CodeRevealDisabled, decodeProblem(t, w).Code)
		assert.Equal(t, "hashed", w.Header().Get(headerFanwork))
		assert.Equal(t, http.StatusOK, get(h, "/api/v1/healthz").Code)
	}
}
