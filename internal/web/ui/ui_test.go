package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testBuild is a fake UI build with the shapes SvelteKit emits.
func testBuild() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                         {Data: []byte("<!doctype html><title>fixture app</title>")},
		"favicon.png":                        {Data: []byte("fixture png")},
		"robots.txt":                         {Data: []byte("User-agent: *\n")},
		"_app/version.json":                  {Data: []byte(`{"version":"1"}`)},
		"_app/immutable/entry/app.ab12.js":   {Data: []byte("console.log('fixture')")},
		"_app/immutable/assets/app.cd34.css": {Data: []byte("body{}")},
		".hidden":                            {Data: []byte("skipped")},
		"_app/.cache/x.js":                   {Data: []byte("skipped")},
	}
}

func newTest(t *testing.T, fsys fstest.MapFS) *Handler {
	t.Helper()
	h, err := NewFS(fsys)
	require.NoError(t, err)
	return h
}

func do(h http.Handler, method, target string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func assertSecurityHeaders(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	assert.Equal(t, CSP, w.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "no-referrer", w.Header().Get("Referrer-Policy"))
	assert.Equal(t, "same-origin", w.Header().Get("Cross-Origin-Resource-Policy"))
	assert.Equal(t, "same-origin", w.Header().Get("Cross-Origin-Opener-Policy"))
}

func TestServeFiles(t *testing.T) {
	h := newTest(t, testBuild())
	assert.True(t, h.Built)
	cases := []struct {
		target, contentType, cache, body string
	}{
		{"/index.html", "text/html; charset=utf-8", cacheRevalidate, "fixture app"},
		{"/favicon.png", "image/png", cacheRevalidate, "fixture png"},
		{"/robots.txt", "text/plain; charset=utf-8", cacheRevalidate, "User-agent"},
		{"/_app/version.json", "application/json", cacheRevalidate, `"version"`},
		{"/_app/immutable/entry/app.ab12.js", "text/javascript; charset=utf-8", cacheImmutable, "fixture"},
		{"/_app/immutable/assets/app.cd34.css", "text/css; charset=utf-8", cacheImmutable, "body{}"},
		{"/_app/immutable/entry/app.ab12.js?v=1", "text/javascript; charset=utf-8", cacheImmutable, "fixture"},
	}
	for _, c := range cases {
		t.Run(c.target, func(t *testing.T) {
			w := do(h, http.MethodGet, c.target)
			require.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, c.contentType, w.Header().Get("Content-Type"))
			assert.Equal(t, c.cache, w.Header().Get("Cache-Control"))
			assert.Contains(t, w.Body.String(), c.body)
			assert.Regexp(t, `^"[0-9a-f]{32}"$`, w.Header().Get("ETag"))
			assert.Empty(t, w.Header().Get("Last-Modified"))
			assertSecurityHeaders(t, w)
		})
	}
}

func TestSPAFallback(t *testing.T) {
	h := newTest(t, testBuild())
	for _, target := range []string{"/", "/works/1", "/works/1/chapters/2", "/signup", "/account/", "/works?x=1"} {
		w := do(h, http.MethodGet, target)
		require.Equal(t, http.StatusOK, w.Code, target)
		assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"), target)
		assert.Equal(t, cacheRevalidate, w.Header().Get("Cache-Control"), target)
		assert.Contains(t, w.Body.String(), "fixture app", target)
		assertSecurityHeaders(t, w)
	}
}

func TestMissingAssets(t *testing.T) {
	h := newTest(t, testBuild())
	for _, target := range []string{
		"/_app/immutable/entry/missing.js", "/_app/", "/_app/immutable", "/missing.png", "/works/1/x.js",
		"/.hidden", "/_app/.cache/x.js", "/_app/immutable/../version.json.bak",
	} {
		w := do(h, http.MethodGet, target)
		assert.Equal(t, http.StatusNotFound, w.Code, target)
		assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"), target)
		assert.Equal(t, "Not Found\n", w.Body.String(), target)
		assertSecurityHeaders(t, w)
	}
}

func TestMethods(t *testing.T) {
	h := newTest(t, testBuild())
	w := do(h, http.MethodHead, "/works/1")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.String())
	assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))

	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions, "QUERY"} {
		w := do(h, m, "/index.html")
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code, m)
		assert.Equal(t, "GET, HEAD", w.Header().Get("Allow"), m)
		assertSecurityHeaders(t, w)
	}
}

func TestConditionalGet(t *testing.T) {
	h := newTest(t, testBuild())
	etag := do(h, http.MethodGet, "/").Header().Get("ETag")
	w := do(h, http.MethodGet, "/works/1", "If-None-Match", etag)
	assert.Equal(t, http.StatusNotModified, w.Code)
	assert.Empty(t, w.Body.String())

	other := do(h, http.MethodGet, "/favicon.png").Header().Get("ETag")
	assert.NotEqual(t, etag, other)
}

func TestPlaceholder(t *testing.T) {
	h := newTest(t, fstest.MapFS{".gitkeep": {}})
	assert.False(t, h.Built)
	for _, target := range []string{"/", "/works/1"} {
		w := do(h, http.MethodGet, target)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
		assert.Contains(t, w.Body.String(), "UI not built")
		assertSecurityHeaders(t, w)
	}
	assert.Equal(t, http.StatusNotFound, do(h, http.MethodGet, "/_app/immutable/x.js").Code)
}

// The placeholder must work under the UI CSP: no inline script, style or event handler.
func TestPlaceholderCSPClean(t *testing.T) {
	page := strings.ToLower(string(placeholder))
	for _, bad := range []string{"<script", "<style", "style=", " on", "javascript:", "http:", "https:"} {
		assert.NotContains(t, page, bad)
	}
}

func TestBuildWithoutIndex(t *testing.T) {
	_, err := NewFS(fstest.MapFS{"_app/immutable/x.js": {Data: []byte("x")}})
	require.ErrorContains(t, err, "no index.html")
}

func TestEmbedded(t *testing.T) {
	h, err := New()
	require.NoError(t, err)
	w := do(h, http.MethodGet, "/")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
	if !h.Built {
		assert.Contains(t, w.Body.String(), "UI not built")
	}
}

func TestContentTypeUnknown(t *testing.T) {
	h := newTest(t, fstest.MapFS{"index.html": {}, "data.bin": {Data: []byte{0}}, "UPPER.JS": {Data: []byte("x")}})
	assert.Equal(t, "application/octet-stream", do(h, http.MethodGet, "/data.bin").Header().Get("Content-Type"))
	assert.Equal(t, "text/javascript; charset=utf-8", do(h, http.MethodGet, "/UPPER.JS").Header().Get("Content-Type"))
}
