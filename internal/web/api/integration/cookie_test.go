package integration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

// The cookie transport (Part 4 stage 1): the web UI's login, refresh and logout with the
// session's tokens in HttpOnly cookies, and the CSRF check on cookie requests.

const cookieRefreshBody = `{"transport":"cookie"}`

func cookieTOTPBody(challenge, code string) string {
	return `{"challenge_token":"` + challenge + `","code":"` + code + `","transport":"cookie"}`
}

// browser sends requests the way the web UI does: same-origin, with the CSRF header, carrying
// the session cookies the server last set (a minimal cookie jar).
type browser struct {
	t       *testing.T
	h       http.Handler
	cookies map[string]string
}

func newBrowser(t *testing.T, h http.Handler) *browser {
	t.Helper()
	return &browser{t: t, h: h, cookies: map[string]string{}}
}

// do sends a request with the browser's cookies and headers; edit may change it first.
func (b *browser) do(method, target, body string, edit ...func(*http.Request)) *httptest.ResponseRecorder {
	b.t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", runtime.ContentTypeJSON)
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set(runtime.HeaderCSRF, runtime.CSRFValue)
	for name, value := range b.cookies {
		req.Header.Add("Cookie", name+"="+value) // a request cookie is only a name and value
	}
	for _, e := range edit {
		e(req)
	}
	w := testutil.Do(b.t, b.h, req)
	for _, c := range w.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c.Value
		}
	}
	return w
}

// setCookies are w's Set-Cookie values by name.
func setCookies(w *httptest.ResponseRecorder) map[string]*http.Cookie {
	out := map[string]*http.Cookie{}
	for _, c := range w.Result().Cookies() {
		out[c.Name] = c
	}
	return out
}

// requireCookieSession requires a 200 cookie session whose refresh cookie lasts refreshTTL:
// no token in the body, both cookies HttpOnly, Secure, SameSite=Strict, Path=/ and no Domain
// (what the __Host- prefix needs). It returns the cookies.
func requireCookieSession(t *testing.T, w *httptest.ResponseRecorder, refreshTTL time.Duration) map[string]*http.Cookie {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	assert.JSONEq(t, `{"transport":"cookie","expires_in":900,"refresh_expires_at":"`+
		seedTime.Add(refreshTTL).Format(time.RFC3339)+`"}`, w.Body.String())
	got := setCookies(w)
	require.Len(t, got, 2, w.Header().Values("Set-Cookie"))
	for name, maxAge := range map[string]int{runtime.CookieAccess: 15 * 60, runtime.CookieRefresh: int(refreshTTL / time.Second)} {
		c := got[name]
		require.NotNil(t, c, name)
		assert.True(t, c.HttpOnly, name)
		assert.True(t, c.Secure, name)
		assert.Equal(t, http.SameSiteStrictMode, c.SameSite, name)
		assert.Equal(t, "/", c.Path, name)
		assert.Empty(t, c.Domain, name)
		assert.Equal(t, maxAge, c.MaxAge, name)
	}
	assert.True(t, strings.HasPrefix(got[runtime.CookieAccess].Value, "eyJ"), "the access token")
	assert.True(t, strings.HasPrefix(got[runtime.CookieRefresh].Value, "rt_"), "the refresh token")
	return got
}

func TestCookieLogin(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	b := newBrowser(t, a.Public())

	challenge := login(t, a.Public(), seedUserName)
	logs.Reset()
	requireCookieSession(t, b.do(http.MethodPost, route.AuthLoginTOTP, cookieTOTPBody(challenge, seedTOTPCode)), 720*time.Hour)
	assert.Equal(t, "cookie", testutil.AccessLine(t, logs)["auth_transport"])
	assert.NotContains(t, logs.String(), b.cookies[runtime.CookieAccess])
	assert.NotContains(t, logs.String(), b.cookies[runtime.CookieRefresh])

	// The access cookie signs requests in; a GET needs no CSRF header.
	logs.Reset()
	w := b.do(http.MethodGet, route.Account, "", func(r *http.Request) { r.Header.Del(runtime.HeaderCSRF) })
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	line := testutil.AccessLine(t, logs)
	assert.Equal(t, "ok", line["auth"])
	assert.Equal(t, "cookie", line["auth_transport"])
}

func TestCookieRefresh(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	b := newBrowser(t, a.Public())
	// seedSession's refresh token, as if a cookie login had set it.
	b.cookies[runtime.CookieRefresh] = seedRefreshToken
	refreshTTL := 720*time.Hour - time.Minute // capped at seedSession's login plus max_session_age

	first := requireCookieSession(t, b.do(http.MethodPost, route.AuthRefresh, cookieRefreshBody), refreshTTL)
	assert.NotEqual(t, seedRefreshToken, first[runtime.CookieRefresh].Value, "rotated")
	require.Equal(t, http.StatusOK, b.do(http.MethodGet, route.Account, "").Code)
	requireCookieSession(t, b.do(http.MethodPost, route.AuthRefresh, cookieRefreshBody), refreshTTL)

	// The first refresh cookie again is reuse: the session is revoked, so the current cookies
	// stop working too.
	stale := newBrowser(t, a.Public())
	stale.cookies[runtime.CookieRefresh] = first[runtime.CookieRefresh].Value
	w := stale.do(http.MethodPost, route.AuthRefresh, cookieRefreshBody)
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidToken.Message())
	requireTokenRefused(t, b.do(http.MethodGet, route.Account, ""))
	w = b.do(http.MethodPost, route.AuthRefresh, cookieRefreshBody)
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidToken.Message())

	// Without the refresh cookie, or with a token in the body as well.
	w = newBrowser(t, a.Public()).do(http.MethodPost, route.AuthRefresh, cookieRefreshBody)
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailNoRefreshCookie.Message())
	w = b.do(http.MethodPost, route.AuthRefresh, `{"transport":"cookie","refresh_token":"`+seedRefreshToken+`"}`)
	assert.Equal(t, []string{"/refresh_token"}, keys(problemPointers(t, w, "token and cookie")))
}

func TestCookieLogout(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	b := newBrowser(t, a.Public())
	requireCookieSession(t, b.do(http.MethodPost, route.AuthLoginTOTP, cookieTOTPBody(seedChallengeToken, seedTOTPCode)),
		720*time.Hour)
	access := b.cookies[runtime.CookieAccess]

	w := b.do(http.MethodPost, route.AuthLogout, "")
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	got := setCookies(w)
	require.Len(t, got, 2)
	for _, name := range []string{runtime.CookieAccess, runtime.CookieRefresh} {
		require.NotNil(t, got[name], name)
		assert.Empty(t, got[name].Value, name)
		assert.Negative(t, got[name].MaxAge, "Max-Age=0: %s", name)
		assert.True(t, got[name].HttpOnly && got[name].Secure, name)
		assert.Equal(t, "/", got[name].Path, name)
	}
	assert.Empty(t, b.cookies, "the browser dropped both")

	// The old access cookie is refused: the session is gone.
	b.cookies[runtime.CookieAccess] = access
	requireTokenRefused(t, b.do(http.MethodGet, route.Account, ""))

	// A bearer logout sets no cookies.
	w = postJSON(t, signedIn(t, a), route.AuthLogout, "")
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Empty(t, w.Header().Values("Set-Cookie"))
}

// TestCookieCSRF: an unsafe request carrying a session cookie, and a cookie-transport login or
// refresh, needs the CSRF header and a same-origin browser request; GET and HEAD don't.
func TestCookieCSRF(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	b := newBrowser(t, a.Public())

	noHeader := func(r *http.Request) { r.Header.Del(runtime.HeaderCSRF) }
	// A cookie login without the header is refused before the challenge is spent.
	body := cookieTOTPBody(seedChallengeToken, seedTOTPCode)
	requireProblem(t, b.do(http.MethodPost, route.AuthLoginTOTP, body, noHeader), dto.TypeForbidden,
		runtime.DetailCSRFHeader.Message())
	requireCookieSession(t, b.do(http.MethodPost, route.AuthLoginTOTP, body), 720*time.Hour)

	// Go's CrossOriginProtection (crossOrigin, every unsafe request) refuses what a browser
	// marks cross-origin; the cookie check also refuses what it lets through (a user-initiated
	// request, or no browser metadata at all).
	goRefused, cookieRefused := runtime.DetailCrossOrigin.Message(), runtime.DetailCookieCrossOrigin.Message()
	otherOrigin := func(r *http.Request) {
		r.Header.Del("Sec-Fetch-Site")
		r.Header.Set("Origin", "https://fixture.invalid")
	}
	for name, c := range map[string]struct {
		edit func(*http.Request)
		want string
	}{
		"cross-site":        {func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, goRefused},
		"same-site":         {func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") }, goRefused},
		"other origin":      {otherOrigin, goRefused},
		"user-initiated":    {func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "none") }, cookieRefused},
		"no fetch metadata": {func(r *http.Request) { r.Header.Del("Sec-Fetch-Site") }, cookieRefused},
		"fetch site twice":  {func(r *http.Request) { r.Header.Add("Sec-Fetch-Site", "same-origin") }, cookieRefused},
	} {
		w := b.do(http.MethodPost, route.AuthLogout, "", c.edit)
		require.Equal(t, http.StatusForbidden, w.Code, name)
		requireProblem(t, w, dto.TypeForbidden, c.want)
		assert.Empty(t, w.Header().Values("Set-Cookie"), name)
	}
	for name, edit := range map[string]func(*http.Request){
		"none":  noHeader,
		"wrong": func(r *http.Request) { r.Header.Set(runtime.HeaderCSRF, "true") },
		"twice": func(r *http.Request) { r.Header.Add(runtime.HeaderCSRF, runtime.CSRFValue) },
	} {
		w := b.do(http.MethodPost, route.AuthLogout, "", edit)
		require.Equal(t, http.StatusForbidden, w.Code, name)
		requireProblem(t, w, dto.TypeForbidden, runtime.DetailCSRFHeader.Message())
	}
	// The same checks on a cookie refresh (whose route needs no access token).
	requireProblem(t, b.do(http.MethodPost, route.AuthRefresh, cookieRefreshBody, noHeader), dto.TypeForbidden,
		runtime.DetailCSRFHeader.Message())
	requireProblem(t, newBrowser(t, a.Public()).do(http.MethodPost, route.AuthRefresh, cookieRefreshBody,
		func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "none") }),
		dto.TypeForbidden, cookieRefused)

	// GET and HEAD need neither (SameSite=Strict keeps the cookies off cross-site requests).
	// The API routes no HEAD, so it is 405, not 403.
	for method, want := range map[string]int{http.MethodGet: http.StatusOK, http.MethodHead: http.StatusMethodNotAllowed} {
		assert.Equal(t, want, b.do(method, route.Account, "", noHeader, otherOrigin).Code, method)
	}

	// A browser without Sec-Fetch-Site passes with an Origin naming this host.
	w := b.do(http.MethodPost, route.AuthLogout, "", func(r *http.Request) {
		r.Header.Del("Sec-Fetch-Site")
		r.Header.Set("Origin", "http://example.com")
	})
	assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
}

// TestCookieCredentials: the access cookie alongside Authorization is ambiguous, a malformed
// access cookie is 400, and requests without cookies are unaffected.
func TestCookieCredentials(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	token := tokenFor(t, a)
	b := newBrowser(t, h)
	b.cookies[runtime.CookieAccess] = token

	w := b.do(http.MethodGet, route.Account, "", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) })
	requireProblem(t, w, dto.TypeBadRequest, runtime.DetailBothCredentials.Message())
	assert.Equal(t, runtime.WWWAuthenticate+`, error="invalid_request"`, w.Header().Get("WWW-Authenticate"))

	for name, edit := range map[string]func(*http.Request){
		"twice":    func(r *http.Request) { r.Header.Add("Cookie", runtime.CookieAccess+"="+token) },
		"bad char": func(r *http.Request) { r.Header.Set("Cookie", runtime.CookieAccess+"=a=b") },
		"too long": func(r *http.Request) {
			r.Header.Set("Cookie", runtime.CookieAccess+"="+strings.Repeat("a", runtime.MaxAuthorizationBytes+1))
		},
	} {
		logs.Reset()
		w := b.do(http.MethodGet, route.Account, "", edit)
		require.Equal(t, http.StatusBadRequest, w.Code, name)
		requireProblem(t, w, dto.TypeBadRequest, runtime.DetailBadAccessCookie.Message())
		assert.Equal(t, "malformed", testutil.AccessLine(t, logs)["auth"], name)
	}
	requireTokenRefused(t, b.do(http.MethodGet, route.Account, "", func(r *http.Request) {
		r.Header.Set("Cookie", runtime.CookieAccess+"=fixture.not.ajwt")
	}))

	// Only the refresh cookie is no access token, and it never signs a request in.
	w = b.do(http.MethodGet, route.Account, "", func(r *http.Request) {
		r.Header.Set("Cookie", runtime.CookieRefresh+"="+seedRefreshToken)
	})
	requireProblem(t, w, dto.TypeUnauthorized, runtime.DetailNoAccessToken.Message())

	// Bearer is the default transport, and explicit bearer is the same: tokens in the body, no
	// cookies.
	w = postJSON(t, h, route.AuthLoginTOTP, `{"challenge_token":"`+seedChallengeToken+`","code":"`+seedTOTPCode+`","transport":"bearer"}`)
	requirePair(t, w)
	assert.Empty(t, w.Header().Values("Set-Cookie"))
}

// The admin socket ignores cookies: no cookie sign-in and no cookie refresh there.
func TestCookiesOnAdminSocket(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	b := newBrowser(t, a.Admin())
	b.cookies[runtime.CookieAccess] = tokenFor(t, a)
	b.cookies[runtime.CookieRefresh] = seedRefreshToken

	requireProblem(t, b.do(http.MethodGet, route.Account, ""), dto.TypeUnauthorized, runtime.DetailAdminNoAccount.Message())
	requireProblem(t, b.do(http.MethodPost, route.AuthRefresh, cookieRefreshBody), dto.TypeInvalidCredentials,
		runtime.DetailNoRefreshCookie.Message())
	// Without the CSRF header either: the socket's requests carry no cookies to check.
	w := b.do(http.MethodGet, route.Sources, "", func(r *http.Request) { r.Header.Del(runtime.HeaderCSRF) })
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}
