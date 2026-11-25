package runtime

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

func TestBearerToken(t *testing.T) {
	const jwt = "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiIxIn0.c2ln"
	for name, c := range map[string]struct {
		header []string
		token  string
		res    authResult
	}{
		"none":          {nil, "", authMissing},
		"basic":         {[]string{"Basic Zml4dHVyZQ=="}, "", authMissing},
		"scheme prefix": {[]string{"Bearerx " + jwt}, "", authMissing},
		"no space":      {[]string{"Bearer"}, "", authMalformed},
		"empty":         {[]string{"Bearer "}, "", authMalformed},
		"only padding":  {[]string{"Bearer =="}, "", authMalformed},
		"two tokens":    {[]string{"Bearer a b"}, "", authMalformed},
		"inner =":       {[]string{"Bearer a=b"}, "", authMalformed},
		"bad char":      {[]string{"Bearer a,b"}, "", authMalformed},
		"non-ASCII":     {[]string{"Bearer é"}, "", authMalformed},
		"twice":         {[]string{"Bearer " + jwt, "Bearer " + jwt}, "", authMalformed},
		"too long":      {[]string{"Bearer " + strings.Repeat("a", MaxAuthorizationBytes)}, "", authMalformed},
		"jwt":           {[]string{"Bearer " + jwt}, jwt, authOK},
		"lower case":    {[]string{"bearer " + jwt}, jwt, authOK},
		"spaces":        {[]string{"BEARER   " + jwt}, jwt, authOK},
		"token68 chars": {[]string{"Bearer aZ09-._~+/=="}, "aZ09-._~+/==", authOK},
		"at the limit": {[]string{"Bearer " + strings.Repeat("a", MaxAuthorizationBytes-len("Bearer "))},
			strings.Repeat("a", MaxAuthorizationBytes-len("Bearer ")), authOK},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		for _, v := range c.header {
			r.Header.Add("Authorization", v)
		}
		token, _, res := bearerToken(r)
		assert.Equal(t, c.res, res, name)
		assert.Equal(t, c.token, token, name)
	}
}

// TestAuthenticate checks which public requests need a token: an anonymous route's path for
// any method passes (so its 405 stays a 405), any other route or unrouted path is refused
// before routing, and the admin socket never asks. A request without a token never reaches
// the Auth controller (these tests have none); the integration tests cover token checks.
func TestAuthenticate(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, func(t *testing.T, opts Options, log *slog.Logger) (*API, error) {
		t.Helper()
		return testNewAnonymous(t, opts, log, func(p string) bool { return p == route.Healthz })
	}, nil)
	for _, admin := range []bool{false, true} {
		a.Router(admin).Get("/private", a.Handle(func(*http.Request) (any, error) {
			return dto.Health{Status: dto.HealthOK}, nil
		}))
	}
	do := func(h http.Handler, method, target, authorization string) *httptest.ResponseRecorder {
		logs.Reset()
		req := httptest.NewRequest(method, target, nil)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		return testutil.Do(t, h, req)
	}

	w := do(a.Public(), http.MethodGet, route.Healthz, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, testutil.AccessLine(t, logs), "auth", "an anonymous route logs no auth outcome")
	assert.Equal(t, http.StatusMethodNotAllowed, do(a.Public(), http.MethodDelete, route.Healthz, "").Code)

	for _, target := range []string{"/private", "/nope", "/private?" + RevealParam + "=1"} {
		w = do(a.Public(), http.MethodGet, target, "")
		require.Equal(t, http.StatusUnauthorized, w.Code, target)
		p := testutil.DecodeProblem(t, w)
		assert.Equal(t, dto.TypeUnauthorized, p.Type, target)
		assert.Equal(t, DetailNoAccessToken.Message(), p.Detail, target)
		assert.Equal(t, WWWAuthenticate, w.Header().Get("WWW-Authenticate"), target)
		assert.Equal(t, "hashed", w.Header().Get(HeaderFanwork), target)
		line := testutil.AccessLine(t, logs)
		assert.Equal(t, string(authMissing), line["auth"], target)
		assert.Equal(t, false, line["reveal"], "refused before reveal: %s", target)
	}
	w = do(a.Public(), http.MethodGet, "/private", "Bearer a b")
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, WWWAuthenticate+`, error="invalid_request"`, w.Header().Get("WWW-Authenticate"))
	assert.Equal(t, string(authMalformed), testutil.AccessLine(t, logs)["auth"])

	w = do(a.Admin(), http.MethodGet, "/private", "Bearer a b")
	assert.Equal(t, http.StatusOK, w.Code, "the admin socket never reads Authorization")
	assert.NotContains(t, testutil.AccessLine(t, logs), "auth")
}

// Outside authenticate a handler gets no principal, only the 401.
func TestPrincipalOutsideAuthenticate(t *testing.T) {
	_, err := Principal(httptest.NewRequest(http.MethodGet, "/", nil))
	p, _ := ProblemFor(err)
	assert.Equal(t, dto.TypeUnauthorized, p.Type)
	assert.Equal(t, DetailNoAccessToken.Message(), p.Detail)

	// Even a state that recorded a refusal has no principal.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	st := &reqState{auth: authInvalid, principal: controllers.Principal{UserID: 1, SessionID: 1}}
	r = r.WithContext(context.WithValue(r.Context(), stateKey{}, st))
	_, err = Principal(r)
	require.Error(t, err)
}
