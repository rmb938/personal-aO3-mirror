package runtime

import (
	"errors"
	"net/http"
	"strings"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
)

// MaxAuthorizationBytes bounds the Authorization header (an access token is about 300 bytes).
const MaxAuthorizationBytes = 2048

// authResult is authenticate's (or NoAccount's) outcome, logged as the access log's auth (a
// closed set).
type authResult string

const (
	authOK        authResult = "ok"
	authMissing   authResult = "missing"   // no bearer credentials (none, or another scheme)
	authMalformed authResult = "malformed" // Authorization repeated, too long or not token68
	authInvalid   authResult = "invalid"   // the token or its session was refused
	authAdmin     authResult = "admin"     // the admin socket, which never reads Authorization
)

// RFC 6750 §3.1 error codes, added to WWW-Authenticate when the request carried credentials.
const (
	bearerInvalidRequest = "invalid_request"
	bearerInvalidToken   = "invalid_token"
)

// bearerError is a refused bearer request: respondError answers its problem and adds the RFC
// 6750 §3.1 error code to WWW-Authenticate.
type bearerError struct {
	code string
	err  *problem.Error
}

func (e *bearerError) Error() string { return e.err.Error() }
func (e *bearerError) Unwrap() error { return e.err }

// bearerToken is the one place a request's access token is read (a cookie transport for the
// Part 3 UI is added here). It returns the token, or authMissing when the request carries no
// bearer credentials (no Authorization, or another scheme) or authMalformed when Authorization
// is sent more than once, is over MaxAuthorizationBytes or holds no single token68 (RFC 9110
// §11.4; RFC 6750 §2.1's b64token).
func bearerToken(r *http.Request) (string, authResult) {
	vals := r.Header.Values("Authorization")
	switch {
	case len(vals) == 0:
		return "", authMissing
	case len(vals) > 1 || len(vals[0]) > MaxAuthorizationBytes:
		return "", authMalformed
	}
	scheme, rest, _ := strings.Cut(vals[0], " ")
	if !strings.EqualFold(scheme, "Bearer") { // RFC 9110 §11.1: the scheme is case-insensitive
		return "", authMissing
	}
	token := strings.TrimLeft(rest, " ")
	if !validToken68(token) {
		return "", authMalformed
	}
	return token, authOK
}

// validToken68 reports whether s is 1*( ALPHA / DIGIT / "-" / "." / "_" / "~" / "+" / "/" )
// *"=" (RFC 9110 §11.2).
func validToken68(s string) bool {
	body := strings.TrimRight(s, "=")
	if body == "" {
		return false
	}
	for i := 0; i < len(body); i++ {
		c := body[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '-' || c == '.' || c == '_' || c == '~' || c == '+' || c == '/'
		if !ok {
			return false
		}
	}
	return true
}

// authenticate is the public listener's access-token check (stage 16g). It runs before
// routing, after crossOrigin and before reveal, so a request without a valid token is always
// hashed. A path whose route pattern (for any method, so a 405 stays a 405) is anonymous
// passes untouched; every other path, including one that matches no route, needs a token.
//
// It refuses a request without a valid access token and gives the handler its Principal. No
// bearer credentials is 401 unauthorized; a malformed Authorization 400 bad-request with
// error="invalid_request"; a refused token (bad JWT, revoked or expired session, disabled or
// deleted account) 401 unauthorized with error="invalid_token" (RFC 6750 §3.1).
func (a *API) authenticate(anonymous func(pattern string) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if anonymous(pathPattern(r)) {
				next.ServeHTTP(w, r)
				return
			}
			st := state(r.Context())
			token, res := bearerToken(r)
			switch res {
			case authMissing:
				st.auth = res
				respondError(w, r, a.log, problem.WithDetail(dto.TypeUnauthorized, DetailNoAccessToken))
				return
			case authMalformed:
				st.auth = res
				respondError(w, r, a.log, &bearerError{code: bearerInvalidRequest,
					err: problem.WithDetail(dto.TypeBadRequest, DetailBadAuthorization)})
				return
			}
			p, err := a.ctl.Auth().Authenticate(r.Context(), token)
			switch {
			case errors.Is(err, controllers.ErrInvalidToken):
				st.auth = authInvalid
				if te, ok := errors.AsType[*auth.TokenError](err); ok {
					st.tokenReason = te.Reason // the session refusals are the controller's auth event
				}
				respondError(w, r, a.log, &bearerError{code: bearerInvalidToken,
					err: problem.WithDetail(dto.TypeUnauthorized, DetailBadAccessToken)})
				return
			case err != nil:
				respondError(w, r, a.log, err) // the users DB: 503 or 500
				return
			}
			st.auth, st.principal = authOK, p
			next.ServeHTTP(w, r)
		})
	}
}

// NoAccount is the admin socket's answer on the signed-in user's routes: always 401. The
// socket never reads Authorization (its file permissions are the boundary) and has no user.
func (a *API) NoAccount(http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state(r.Context()).auth = authAdmin
		respondError(w, r, a.log, problem.WithDetail(dto.TypeUnauthorized, DetailAdminNoAccount))
	})
}

// Principal is the signed-in user authenticate admitted. Outside it (an anonymous route, the
// admin socket, a handler unit test) it is the 401 unauthorized error, so a handler that needs
// a user can never run without one.
func Principal(r *http.Request) (controllers.Principal, error) {
	st := state(r.Context())
	if st.auth != authOK {
		return controllers.Principal{}, problem.WithDetail(dto.TypeUnauthorized, DetailNoAccessToken)
	}
	return st.principal, nil
}
