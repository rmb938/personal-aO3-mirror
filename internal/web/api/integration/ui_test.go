package integration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
	"github.com/rmb938/personal-aO3-mirror/internal/web/ui"
)

const (
	uiIndex = "<!doctype html><title>fixture app</title>"
	uiAsset = "/_app/immutable/entry/app.ab12.js"
)

// testUI is a fake UI build served by every test API's public listener.
func testUI(t *testing.T) *ui.Handler {
	t.Helper()
	h, err := ui.NewFS(fstest.MapFS{
		"index.html":        {Data: []byte(uiIndex)},
		uiAsset[1:]:         {Data: []byte("console.log('fixture')")},
		"_app/version.json": {Data: []byte(`{"version":"1"}`)},
	})
	require.NoError(t, err)
	return h
}

func TestUIServedOnPublic(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()

	w := testutil.Get(t, h, "/works/1")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, uiIndex, w.Body.String())
	assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Equal(t, ui.CSP, w.Header().Get("Content-Security-Policy"), "ui's CSP replaces the API's")
	assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.NotEmpty(t, w.Header().Get(runtime.HeaderRequestID))
	assert.Empty(t, w.Header().Get(runtime.HeaderFanwork), "outside the enforcer")

	line := testutil.AccessLine(t, logs)
	assert.Equal(t, runtime.UIRoute, line["route"])
	assert.Equal(t, "public", line["listener"])
	assert.InDelta(t, http.StatusOK, line["status"], 0)
	assert.NotContains(t, logs.String(), "/works/1", "the raw path is never logged")

	w = testutil.Get(t, h, uiAsset)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "public, max-age=31536000, immutable", w.Header().Get("Cache-Control"))
	assert.Equal(t, http.StatusNotFound, testutil.Get(t, h, "/_app/immutable/missing.js").Code)
}

func TestUIDoesNotShadowAPI(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	w := testutil.Get(t, h, route.Healthz)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, runtime.ContentTypeJSON, w.Header().Get("Content-Type"))
	assert.Equal(t, "hashed", w.Header().Get(runtime.HeaderFanwork))

	// Every spelling of a path under /api/, routed or not, stays with the API (and its
	// enforcer and auth): never the UI's index page.
	for _, target := range []string{"/api", "/api/", "/api/v1", "/api/v1/nope", "/api/index.html", "/%61pi/v1/works", "/api%2Fv1/works"} {
		w := testutil.Get(t, h, target)
		assert.Equal(t, http.StatusUnauthorized, w.Code, target)
		assert.Equal(t, dto.TypeUnauthorized, testutil.DecodeProblem(t, w).Type, target)
		assert.NotContains(t, w.Body.String(), "fixture app", target)
	}
	// Close but not /api/: the UI.
	for _, target := range []string{"/apix", "/API/v1/works", "//api/v1/works"} {
		w := testutil.Get(t, h, target)
		assert.Equal(t, http.StatusOK, w.Code, target)
		assert.Equal(t, uiIndex, w.Body.String(), target)
	}
}

func TestUINotOnAdmin(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	w := testutil.Get(t, a.Admin(), "/works/1")
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, dto.TypeNotFound, testutil.DecodeProblem(t, w).Type)
}

func TestUIWithoutOption(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, func(o *runtime.Options) { o.UI = nil })
	w := testutil.Get(t, a.Public(), "/works/1")
	assert.Equal(t, http.StatusUnauthorized, w.Code, "every path is the API's")
}

func TestUIMiddleware(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, func(o *runtime.Options) { o.UIPerMin = 2 })
	h := a.Public()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "evil.example"
	w := testutil.Do(t, h, req)
	assert.Equal(t, http.StatusMisdirectedRequest, w.Code, "Host check")
	assert.Equal(t, dto.TypeMisdirected, testutil.DecodeProblem(t, w).Type)
	assert.Equal(t, "default-src 'none'; frame-ancestors 'none'", w.Header().Get("Content-Security-Policy"))

	w = testutil.Get(t, h, "/"+strings.Repeat("a", runtime.MaxURLBytes))
	assert.Equal(t, http.StatusRequestURITooLong, w.Code, "URL limits")

	// The two rejections above come before the rate limit, so the budget of 2 is untouched.
	w = testutil.Do(t, h, httptest.NewRequest(http.MethodPost, "/works/1", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Equal(t, "GET, HEAD", w.Header().Get("Allow"))
	require.Equal(t, http.StatusOK, testutil.Get(t, h, "/").Code)
	w = testutil.Get(t, h, "/")
	assert.Equal(t, http.StatusTooManyRequests, w.Code, "the UI's own rate limit")
	assert.Equal(t, dto.TypeRateLimited, testutil.DecodeProblem(t, w).Type)
	assert.Equal(t, http.StatusOK, testutil.Get(t, h, route.Healthz).Code, "the API has its own budget")
}

// The UI's budget is UIPerMin, not GeneralPerMin: a page load's many files don't spend the
// API's budget, and a small API budget doesn't limit them.
func TestUIRateBudget(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, func(o *runtime.Options) { o.GeneralPerMin, o.UIPerMin = 1, 5 })
	h := a.Public()
	for i := range 5 {
		require.Equal(t, http.StatusOK, testutil.Get(t, h, "/").Code, "UI request %d", i+1)
	}
	assert.Equal(t, http.StatusTooManyRequests, testutil.Get(t, h, "/").Code, "the sixth is over UIPerMin")
	assert.Equal(t, http.StatusOK, testutil.Get(t, h, route.Healthz).Code, "the API's budget of 1 is unspent")
	assert.Equal(t, http.StatusTooManyRequests, testutil.Get(t, h, route.Healthz).Code, "and then spent")
}

// TestUILeak is the all-routes leak test's UI part: requests carrying sentinels in the path,
// query, headers and body never get them echoed in the response or written to the logs.
func TestUILeak(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	targets := []string{
		"/" + sentinel + "path",
		"/works/" + sentinel + "1?" + sentinel + "name=" + sentinel + "value",
		"/_app/immutable/" + sentinel + ".js",
		"/" + sentinel + ".png",
		uiAsset + "?" + sentinel + "q=1",
		"/?" + runtime.RevealParam + "=1&" + sentinel + "x=1",
	}
	for _, target := range targets {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, "QUERY"} {
			req := httptest.NewRequest(method, target, strings.NewReader(`{"x":"`+sentinel+`body"}`))
			req.Header.Set(runtime.HeaderRequestID, sentinel+" not a valid id")
			req.Header.Set("Referer", "https://example.com/"+sentinel)
			req.Header.Set("Cookie", "__Host-ao3mirror-access="+sentinel)
			w := testutil.Do(t, h, req)
			assert.Less(t, w.Code, 500, "%s %s", method, target)
			testutil.AssertNoSentinel(t, w, logs, sentinel)
		}
	}
}
