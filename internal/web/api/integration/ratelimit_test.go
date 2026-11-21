package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

// Load tests for the rate limits (docs/part2-api.md, stage 15b): concurrent in-process bursts
// per limit group and listener. httprate checks and counts under one lock, and a fresh
// limiter's window starts when it is built, so a burst is exact: limit requests pass, the
// rest are 429.

const (
	// loadLimit is each group's public limit under test; the admin socket allows
	// runtime.AdminRateFactor times as many.
	loadLimit = 5
	// burstFactor is how many times its limit a burst sends. An admin burst
	// (loadLimit*AdminRateFactor*burstFactor requests) stays under the throttle's in-flight
	// plus backlog cap, so no request is a 503.
	burstFactor = 3
)

// limitGroup is one rate limit: the option that sets it, its window, and requests that count
// against it.
type limitGroup struct {
	set    func(o *runtime.Options, n int)
	window time.Duration
	// okCode is the status of a request the limiter lets through.
	okCode int
	// request builds a burst's i-th request. Requests rotate through the group's routes, so
	// the burst also checks that those routes share one budget.
	request func(i int) *http.Request
}

func jsonRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", runtime.ContentTypeJSON)
	return req
}

var limitGroups = map[string]limitGroup{
	"general": {
		set:    func(o *runtime.Options, n int) { o.GeneralPerMin = n },
		window: time.Minute,
		okCode: http.StatusOK,
		request: func(i int) *http.Request {
			targets := []string{route.Healthz, route.Version, route.Sources, route.Works, route.Path(route.Tag, seedTag)}
			return httptest.NewRequest(http.MethodGet, targets[i%len(targets)], nil)
		},
	},
	"search": {
		set:    func(o *runtime.Options, n int) { o.SearchPerMin = n },
		window: time.Minute,
		okCode: http.StatusOK,
		request: func(i int) *http.Request {
			targets := []string{
				route.Works, route.Tags, route.Creators,
				route.Path(route.TagWorks, seedTag),
				route.Path(route.CreatorWorks, seedCreator),
				route.Path(route.CollectionWorks, seedCollection),
			}
			return jsonRequest(runtime.MethodQuery, targets[i%len(targets)], `{}`)
		},
	},
	"source add": {
		set:    func(o *runtime.Options, n int) { o.SourceAddPerHour = n },
		window: time.Hour,
		okCode: http.StatusAccepted,
		request: func(i int) *http.Request {
			// A distinct tag per request, so every add that passes is queued (202).
			return jsonRequest(http.MethodPost, route.Sources, `{"url":"/tags/load`+strconv.Itoa(i)+`/works"}`)
		},
	},
}

// burst sends m requests to h at once (each goroutine waits on one start signal) and returns
// the responses in request order.
func burst(t *testing.T, h http.Handler, m int, request func(i int) *http.Request) []*httptest.ResponseRecorder {
	t.Helper()
	reqs := make([]*http.Request, m)
	ws := make([]*httptest.ResponseRecorder, m)
	for i := range m {
		reqs[i] = request(i)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range m {
		wg.Go(func() {
			<-start
			ws[i] = testutil.Do(t, h, reqs[i])
		})
	}
	close(start)
	wg.Wait()
	return ws
}

// requireBurst asserts exactly n responses are okCode and the rest are rate-limited problems
// with Retry-After set to the window.
func requireBurst(t *testing.T, ws []*httptest.ResponseRecorder, n, okCode int, window time.Duration, msg string) {
	t.Helper()
	passed := 0
	for i, w := range ws {
		switch w.Code {
		case okCode:
			passed++
		case http.StatusTooManyRequests:
			assert.Equal(t, dto.TypeRateLimited, testutil.DecodeProblem(t, w).Type, "%s: request %d", msg, i)
			assert.Equal(t, strconv.Itoa(int(window.Seconds())), w.Header().Get("Retry-After"), "%s: request %d", msg, i)
			assert.Equal(t, "hashed", w.Header().Get(runtime.HeaderFanwork), "%s: request %d", msg, i)
		default:
			t.Errorf("%s: request %d: status %d: %s", msg, i, w.Code, w.Body.String())
		}
	}
	require.Equal(t, n, passed, "%s: requests through the limiter", msg)
}

func TestRateLimitBurst(t *testing.T) {
	for name, g := range limitGroups {
		t.Run(name, func(t *testing.T) {
			// One API per group: the limiters start fresh, and the others stay at
			// testOptions' 1000, out of the way.
			a, _ := testutil.NewAPI(t, testOptions, testNew, func(o *runtime.Options) { g.set(o, loadLimit) })
			for listener, n := range map[string]int{"public": loadLimit, "admin": loadLimit * runtime.AdminRateFactor} {
				h := a.Public()
				if listener == "admin" {
					h = a.Admin()
				}
				requireBurst(t, burst(t, h, burstFactor*n, g.request), n, g.okCode, g.window, listener)
				// The burst came from one client (httptest's peer, or the socket's fixed
				// bucket); it is now limited, and a route outside the group is not.
				assert.Equal(t, http.StatusTooManyRequests, testutil.Do(t, h, g.request(0)).Code, listener)
				if name != "general" {
					assert.Equal(t, http.StatusOK, testutil.Get(t, h, route.Healthz).Code, "%s: general budget left", listener)
				}
			}
		})
	}
}

// TestRateLimitForwardedFor bursts the general limit with forged and proxied X-Forwarded-For.
func TestRateLimitForwardedFor(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, func(o *runtime.Options) { o.GeneralPerMin = loadLimit })
	h := a.Public()
	get := func(peer, xff string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, route.Healthz, nil)
		req.RemoteAddr = peer
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		return req
	}
	const clients = 3
	client := func(c int) string { return fmt.Sprintf("203.0.113.%d", c+1) }

	// An untrusted peer naming a different client in every request still has one bucket,
	// and its forged clients' buckets are untouched.
	ws := burst(t, h, burstFactor*loadLimit, func(i int) *http.Request {
		return get("198.51.100.7:4000", client(i%clients))
	})
	requireBurst(t, ws, loadLimit, http.StatusOK, time.Minute, "untrusted peer")

	// Behind the trusted proxy each forwarded client has its own full bucket, keyed by the
	// rightmost untrusted entry: a client-supplied entry to its left changes nothing.
	ws = burst(t, h, clients*2*loadLimit, func(i int) *http.Request {
		return get("127.0.0.1:4000", fmt.Sprintf("192.0.2.%d, %s", i+1, client(i%clients)))
	})
	for c := range clients {
		var mine []*httptest.ResponseRecorder
		for i, w := range ws {
			if i%clients == c {
				mine = append(mine, w)
			}
		}
		requireBurst(t, mine, loadLimit, http.StatusOK, time.Minute, client(c))
	}

	// Local clients without X-Forwarded-For (v4 and v6 loopback) share one bucket.
	ws = burst(t, h, burstFactor*loadLimit, func(i int) *http.Request {
		return get([]string{"127.0.0.1:4000", "[::1]:4000"}[i%2], "")
	})
	requireBurst(t, ws, loadLimit, http.StatusOK, time.Minute, "local")
}
