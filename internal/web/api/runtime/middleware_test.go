package runtime

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

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

func TestRequestID(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	w := testutil.Get(t, a.Public(), "/api/v1/healthz")
	id := w.Header().Get(HeaderRequestID)
	assert.Regexp(t, `^[0-9a-f]{16}$`, id)
	assert.Empty(t, w.Header().Get(HeaderClientRequestID))
	assert.Equal(t, id, testutil.AccessLine(t, logs)["req_id"])

	w2 := testutil.Get(t, a.Public(), "/api/v1/healthz")
	assert.NotEqual(t, id, w2.Header().Get(HeaderRequestID), "a fresh ID per request")
}

func TestClientRequestID(t *testing.T) {
	cases := []struct {
		name  string
		vals  []string
		valid bool
	}{
		{"valid", []string{"abc.DEF_123:x-y"}, true},
		{"max length", []string{strings.Repeat("a", MaxClientReqIDBytes)}, true},
		{"too long", []string{strings.Repeat("a", MaxClientReqIDBytes+1)}, false},
		{"bad chars", []string{"a b"}, false},
		{"slash", []string{"a/b"}, false},
		{"empty", []string{""}, false},
		{"repeated", []string{"a", "b"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
			for _, v := range c.vals {
				req.Header.Add(HeaderRequestID, v)
			}
			w := testutil.Do(t, a.Public(), req)
			require.Equal(t, http.StatusOK, w.Code)
			assert.Regexp(t, `^[0-9a-f]{16}$`, w.Header().Get(HeaderRequestID),
				"the server ID is always our own")
			line := testutil.AccessLine(t, logs)
			if c.valid {
				assert.Equal(t, c.vals[0], w.Header().Get(HeaderClientRequestID))
				assert.Equal(t, c.vals[0], line["client_req_id"])
				assert.NotContains(t, line, "client_req_id_invalid")
			} else {
				assert.Empty(t, w.Header().Get(HeaderClientRequestID))
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
			a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
			req.RemoteAddr = c.remote
			if c.xff != nil {
				req.Header["X-Forwarded-For"] = c.xff
			}
			require.Equal(t, http.StatusOK, testutil.Do(t, a.Public(), req).Code)
			assert.Equal(t, c.want, testutil.AccessLine(t, logs)["client_ip"])
		})
	}

	t.Run("admin socket never trusts XFF", func(t *testing.T) {
		a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
		req.RemoteAddr = "@"
		req.Header.Set("X-Forwarded-For", "203.0.113.7")
		require.Equal(t, http.StatusOK, testutil.Do(t, a.Admin(), req).Code)
		assert.Empty(t, testutil.AccessLine(t, logs)["client_ip"])
	})
}

func TestAccessLogIsContentFree(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	a.Router(false).Get("/items/{id}/parts/{position}", a.Handle(func(*http.Request) (any, error) { return nil, problem.New(dto.TypeNotFound) }))

	req := httptest.NewRequest(http.MethodGet, "/items/42/parts/"+sentinel+"part?page="+sentinel+"v&"+sentinel+"n=1", nil)
	req.Header.Set(HeaderFunnel, "?1")
	testutil.Do(t, a.Router(false), req)
	line := testutil.AccessLine(t, logs)
	assert.Equal(t, "/items/{id}/parts/{position}", line["route"])
	assert.Equal(t, "public", line["listener"])
	assert.Equal(t, http.MethodGet, line["method"])
	assert.InDelta(t, 42, line["path_id"], 0)
	assert.Equal(t, true, line["path_position_invalid"])
	assert.Equal(t, []any{"page"}, line["params"])
	assert.InDelta(t, 1, line["other_params"], 0)
	assert.Equal(t, true, line["funnel"])
	assert.Equal(t, false, line["reveal"])
	assert.InDelta(t, http.StatusNotFound, line["status"], 0)

	// A free-text path parameter is never logged, even when it parses as an id.
	a.Router(false).Get("/named/{name}", a.Handle(func(*http.Request) (any, error) { return nil, problem.New(dto.TypeNotFound) }))
	logs.Reset()
	testutil.Do(t, a.Router(false), httptest.NewRequest(http.MethodGet, "/named/9876543210123", nil))
	line = testutil.AccessLine(t, logs)
	assert.Equal(t, "/named/{name}", line["route"])
	assert.NotContains(t, line, "path_name")
	assert.NotContains(t, line, "path_name_invalid")
	assert.NotContains(t, logs.String(), "9876543210123")
	assert.NotContains(t, logs.String(), sentinel)
}

// A request rejected before routing (here, rate limited) still logs its route pattern and
// numeric path IDs, flagged prerouted. An unmatched path logs no route.
func TestAccessLogPreroutedRoute(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, func(o *Options) { o.GeneralPerMin = 1 })
	a.Router(false).Get("/items/{id}/parts/{position}", a.Handle(func(*http.Request) (any, error) {
		return dto.Health{Status: dto.HealthOK}, nil
	}))

	a.Router(false).Get("/named/{name}", a.Handle(func(*http.Request) (any, error) {
		return dto.Health{Status: dto.HealthOK}, nil
	}))

	require.Equal(t, http.StatusOK, testutil.Get(t, a.Router(false), "/items/1/parts/2").Code)
	for _, target := range []string{"/items/42/parts/" + sentinel + "part", "/" + sentinel + "nope", "/named/9876543210123"} {
		require.Equal(t, http.StatusTooManyRequests, testutil.Get(t, a.Router(false), target).Code, target)
	}
	var lines []map[string]any
	for _, l := range testutil.LogLines(t, logs) {
		if l["msg"] == "api request" {
			lines = append(lines, l)
		}
	}
	require.Len(t, lines, 4)
	assert.Nil(t, lines[0]["prerouted"], "routed request")

	limited := lines[1]
	assert.Equal(t, "/items/{id}/parts/{position}", limited["route"])
	assert.Equal(t, true, limited["prerouted"])
	assert.InDelta(t, 42, limited["path_id"], 0)
	assert.Equal(t, true, limited["path_position_invalid"])

	assert.Empty(t, lines[2]["route"], "unmatched path")
	assert.Nil(t, lines[2]["prerouted"])

	named := lines[3]
	assert.Equal(t, "/named/{name}", named["route"])
	assert.Equal(t, true, named["prerouted"])
	assert.NotContains(t, named, "path_name", "a free-text path param is never logged, prerouted either")
	assert.NotContains(t, logs.String(), "9876543210123")
	assert.NotContains(t, logs.String(), sentinel)
}

func TestAccessLogClientMethod(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	w := testutil.Do(t, a.Public(), httptest.NewRequest("SENTINELMETHOD", "/api/v1/healthz", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Equal(t, "other", testutil.AccessLine(t, logs)["method"])
	assert.NotContains(t, logs.String(), "SENTINEL")
}

func TestRecoverer(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	a.Router(false).Get("/panic", func(http.ResponseWriter, *http.Request) { panic(sentinel + "panic value") })
	w := testutil.Get(t, a.Router(false), "/panic")
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, dto.TypeInternal, testutil.DecodeProblem(t, w).Type)
	assert.Equal(t, "hashed", w.Header().Get(HeaderFanwork))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"), "security headers survive a panic")
	assert.Contains(t, logs.String(), `"panic_type":"string"`)
	assert.Contains(t, logs.String(), "stack")
	assert.NotContains(t, logs.String(), sentinel)

	a.Router(false).Get("/abort", func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	assert.PanicsWithValue(t, http.ErrAbortHandler, func() { testutil.Get(t, a.Router(false), "/abort") })
}

func TestSecurityHeaders(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	for _, target := range []string{"/api/v1/healthz", "/api/v1/nope"} {
		h := testutil.Get(t, a.Public(), target).Header()
		assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"))
		assert.Equal(t, "default-src 'none'; frame-ancestors 'none'", h.Get("Content-Security-Policy"))
		assert.Equal(t, "no-referrer", h.Get("Referrer-Policy"))
		assert.Equal(t, "same-origin", h.Get("Cross-Origin-Resource-Policy"))
		assert.Equal(t, "no-store", h.Get("Cache-Control"))
		assert.Empty(t, h.Get("Access-Control-Allow-Origin"))
	}
}

func TestHostCheck(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, func(o *Options) {
		o.AllowedHosts = []string{"example.com", "Mirror.Example.net", "127.0.0.1", "::1"}
	})
	do := func(h http.Handler, host string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
		req.Host = host
		return testutil.Do(t, h, req)
	}
	for _, host := range []string{
		"example.com", "EXAMPLE.com:8080", "mirror.example.net", "127.0.0.1:8080", "[::1]:8080", "[::1]",
	} {
		assert.Equal(t, http.StatusOK, do(a.Public(), host).Code, host)
	}
	for _, host := range []string{"", "evil.example", "example.com.", "example.com.evil.example", "127.0.0.2:8080", "[::1"} {
		w := do(a.Public(), host)
		require.Equal(t, http.StatusMisdirectedRequest, w.Code, host)
		assert.Equal(t, dto.TypeMisdirected, testutil.DecodeProblem(t, w).Type, host)
		assert.Equal(t, "hashed", w.Header().Get(HeaderFanwork), host)
		assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"), host)
	}
	assert.Equal(t, http.StatusOK, do(a.Admin(), "admin").Code, "the socket does not check Host")
}

func TestURLLimits(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	long := "/api/v1/healthz?x=" + strings.Repeat("a", MaxURLBytes)
	w := testutil.Get(t, a.Public(), long)
	require.Equal(t, http.StatusRequestURITooLong, w.Code)
	assert.Equal(t, dto.TypeURITooLong, testutil.DecodeProblem(t, w).Type)

	w = testutil.Get(t, a.Public(), "/"+strings.Repeat("a", MaxURLBytes))
	assert.Equal(t, http.StatusRequestURITooLong, w.Code)
}

func TestRateLimit(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, func(o *Options) { o.GeneralPerMin = 2 })
	fromIP := func(ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
		req.RemoteAddr = ip + ":1234"
		return testutil.Do(t, a.Public(), req)
	}
	assert.Equal(t, http.StatusOK, fromIP("198.51.100.1").Code)
	assert.Equal(t, http.StatusOK, fromIP("198.51.100.1").Code)
	w := fromIP("198.51.100.1")
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, dto.TypeRateLimited, testutil.DecodeProblem(t, w).Type)
	assert.Equal(t, "60", w.Header().Get("Retry-After"))
	assert.Equal(t, "hashed", w.Header().Get(HeaderFanwork))

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
	for range 2 * AdminRateFactor {
		require.Equal(t, http.StatusOK, testutil.Get(t, a.Admin(), "/api/v1/healthz").Code)
	}
	assert.Equal(t, http.StatusTooManyRequests, testutil.Get(t, a.Admin(), "/api/v1/healthz").Code)
}

// blockingThrottle wraps a handler that blocks until release is closed, signalling started.
func blockingThrottle(t *testing.T, backlog int, wait time.Duration) (h http.Handler, started chan struct{}, release chan struct{}) {
	t.Helper()
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
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
	w := testutil.Do(t, h, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, dto.TypeUnavailable, testutil.DecodeProblem(t, w).Type)
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
	w := testutil.Do(t, h, httptest.NewRequest(http.MethodGet, "/", nil))
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
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
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
	w := testutil.Do(t, h, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	close(release)
	wg.Wait()
}

func TestRequestTimeout(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, func(o *Options) { o.RequestTimeout = 20 * time.Millisecond })
	a.Router(false).Get("/slow", a.Handle(func(r *http.Request) (any, error) {
		_, ok := r.Context().Deadline()
		assert.True(t, ok)
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	w := testutil.Get(t, a.Router(false), "/slow")
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, dto.TypeUnavailable, testutil.DecodeProblem(t, w).Type)
}

func TestCrossOrigin(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/healthz", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := testutil.Do(t, a.Public(), req)
	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, dto.TypeForbidden, testutil.DecodeProblem(t, w).Type)

	req = httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	assert.Equal(t, http.StatusOK, testutil.Do(t, a.Public(), req).Code, "safe methods are allowed")

	req = httptest.NewRequest(http.MethodPost, "/api/v1/healthz", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	assert.Equal(t, http.StatusMethodNotAllowed, testutil.Do(t, a.Public(), req).Code)
}

func TestReveal(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	w := testutil.Get(t, a.Public(), "/api/v1/healthz"+revealQuery)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "revealed", w.Header().Get(HeaderFanwork))
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	assert.Empty(t, w.Header().Get(HeaderFanworkEpoch))
	assert.Equal(t, true, testutil.AccessLine(t, logs)["reveal"])

	w = testutil.Get(t, a.Public(), "/api/v1/healthz")
	assert.Equal(t, "hashed", w.Header().Get(HeaderFanwork))
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"), "securityHeaders: every response")
	assert.NotEmpty(t, w.Header().Get(HeaderFanworkEpoch))

	for name, q := range map[string]string{
		"other value": "?" + RevealParam + "=true",
		"empty value": "?" + RevealParam + "=",
		"repeated":    revealQuery + "&" + RevealParam + "=1",
	} {
		t.Run(name, func(t *testing.T) {
			w := testutil.Get(t, a.Public(), "/api/v1/healthz"+q)
			testutil.RequireParamProblem(t, w, dto.ParamReveal, name)
			assert.Equal(t, "hashed", w.Header().Get(HeaderFanwork))
		})
	}
	w = testutil.Get(t, a.Public(), "/api/v1/healthz?%zz")
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, dto.TypeBadRequest, testutil.DecodeProblem(t, w).Type)
}

func TestNoRevealRefusesReveal(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, func(o *Options) { o.NoReveal = true })
	for _, h := range []http.Handler{a.Public(), a.Admin()} {
		w := testutil.Get(t, h, "/api/v1/healthz"+revealQuery)
		require.Equal(t, http.StatusForbidden, w.Code)
		assert.Equal(t, dto.TypeRevealDisabled, testutil.DecodeProblem(t, w).Type)
		assert.Equal(t, "hashed", w.Header().Get(HeaderFanwork))
		assert.Equal(t, http.StatusOK, testutil.Get(t, h, "/api/v1/healthz").Code)
	}
}
