package runtime

import (
	"net/http"
	"net/url"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
)

// The cookie transport (Part 4 stage 1, docs/part4-ui.md "Auth in the browser"): the web UI logs
// in with transport cookie and the server sets the session's tokens as HttpOnly cookies, which
// JS never sees. The server is the only one that sets or clears them.
const (
	// CookieAccess holds the access token; authenticate reads it when Authorization is absent.
	CookieAccess = "__Host-ao3mirror-access"
	// CookieRefresh holds the refresh token; only POST /api/v1/auth/refresh with transport
	// cookie reads it. The __Host- prefix needs Path=/, so the browser sends it on every
	// request; SameSite=Strict and HttpOnly keep it from other sites and from JS.
	CookieRefresh = "__Host-ao3mirror-refresh"
	// HeaderCSRF must be CSRFValue on every request that carries a session cookie, except
	// GET and HEAD, and on a login or refresh asking for the cookie transport. A cross-site page
	// can't set a custom header without a CORS preflight, which the API never grants.
	HeaderCSRF = "X-Ao3mirror-Csrf"
	CSRFValue  = "1"
)

// transport is how a request's session credentials came (the access log's auth_transport, ""
// when none were read).
type transport string

const (
	transportBearer transport = "bearer"
	transportCookie transport = "cookie"
)

// SessionCookies are a cookie-transport login's or refresh's Set-Cookie values: the access
// cookie lives as long as the access token, the refresh cookie until the session ends unless
// refreshed. Both lifetimes are relative (Max-Age), so the client's clock doesn't matter.
func SessionCookies(p controllers.TokenPair) []*http.Cookie {
	issued := p.AccessExpiresAt.Add(-p.AccessTTL)
	return []*http.Cookie{
		sessionCookie(CookieAccess, p.AccessToken, p.AccessTTL),
		sessionCookie(CookieRefresh, p.RefreshToken, p.RefreshExpiresAt.Sub(issued)),
	}
}

// ClearSessionCookies are logout's Set-Cookie values: both cookies, expired.
func ClearSessionCookies() []*http.Cookie {
	return []*http.Cookie{sessionCookie(CookieAccess, "", 0), sessionCookie(CookieRefresh, "", 0)}
}

// sessionCookie is a __Host- cookie (Secure, Path=/, no Domain) that JS can't read and other
// sites can't send. A ttl under a second (0: clear) is Max-Age=0.
func sessionCookie(name, value string, ttl time.Duration) *http.Cookie {
	maxAge := int((ttl + time.Second - 1) / time.Second)
	if maxAge <= 0 {
		maxAge = -1 // net/http writes Max-Age=0
	}
	return &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: maxAge, Secure: true, HttpOnly: true,
		SameSite: http.SameSiteStrictMode}
}

// cookieValues are the values of every cookie named name in r (a Cookie header may repeat one).
func cookieValues(r *http.Request, name string) []string {
	var out []string
	for _, c := range r.Cookies() {
		if c.Name == name {
			out = append(out, c.Value)
		}
	}
	return out
}

// hasSessionCookie reports whether r carries either session cookie.
func hasSessionCookie(r *http.Request) bool {
	return len(cookieValues(r, CookieAccess)) > 0 || len(cookieValues(r, CookieRefresh)) > 0
}

// CheckCSRF is the cookie transport's CSRF check: HeaderCSRF set to CSRFValue (once) and the
// request same-origin, by Sec-Fetch-Site or, from a browser that doesn't send it, by Origin
// naming the request's own host. Either failure is 403 forbidden. It runs on every unsafe
// request carrying a session cookie (cookieCSRF) and on a login or refresh asking for the
// cookie transport, before anything is spent (a challenge try, a refresh token).
func CheckCSRF(r *http.Request) error {
	if v := r.Header.Values(HeaderCSRF); len(v) != 1 || v[0] != CSRFValue {
		return problem.WithDetail(dto.TypeForbidden, DetailCSRFHeader)
	}
	if !sameOrigin(r) {
		return problem.WithDetail(dto.TypeForbidden, DetailCookieCrossOrigin)
	}
	return nil
}

// sameOrigin reports whether a browser marked r same-origin: Sec-Fetch-Site same-origin, or
// without that header an Origin whose host is r.Host. A request with neither is refused, so
// the cookie transport works only from browsers.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Values("Sec-Fetch-Site"); len(site) > 0 {
		return len(site) == 1 && site[0] == "same-origin"
	}
	origin := r.Header.Values("Origin")
	if len(origin) != 1 {
		return false
	}
	u, err := url.Parse(origin[0])
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.Host == r.Host
}

// cookieCSRF runs CheckCSRF on every request carrying a session cookie whose method is not GET
// or HEAD (public listener; the admin socket drops cookies). A request without one (the CLI,
// any bearer client) passes untouched.
func (a *API) cookieCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && hasSessionCookie(r) {
			if err := CheckCSRF(r); err != nil {
				respondError(w, r, a.log, err)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// dropCookies removes the Cookie header: the admin socket never reads cookies (its file
// permissions are its boundary), so no handler there can act on one.
func dropCookies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del("Cookie")
		next.ServeHTTP(w, r)
	})
}

// RefreshCookie is the refresh token of a cookie-transport refresh: the refresh cookie, sent
// once. Without it (or with it repeated, or over MaxAuthorizationBytes) it is 401
// invalid-credentials, like any refused refresh token.
func RefreshCookie(r *http.Request) (string, error) {
	state(r.Context()).transport = transportCookie
	vals := cookieValues(r, CookieRefresh)
	if len(vals) != 1 || vals[0] == "" || len(vals[0]) > MaxAuthorizationBytes {
		return "", problem.WithDetail(dto.TypeInvalidCredentials, DetailNoRefreshCookie)
	}
	return vals[0], nil
}

// UseCookieTransport records that a login answers with the cookie transport (the access log's
// auth_transport).
func UseCookieTransport(r *http.Request) { state(r.Context()).transport = transportCookie }

// CookieAuth reports whether authenticate admitted r by its access cookie (so logout clears
// the cookies).
func CookieAuth(r *http.Request) bool {
	st := state(r.Context())
	return st.auth == authOK && st.transport == transportCookie
}
