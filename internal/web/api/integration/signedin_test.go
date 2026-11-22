package integration

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	api "github.com/rmb938/personal-aO3-mirror/internal/web/api"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

// apiTokens holds, for each API newAPI built, a function returning an access token of
// seedSession on it, made at first use (stage 16g: every public route but the anonymous ones
// needs one).
var apiTokens sync.Map // *runtime.API → func() (string, error)

// newAPI is api.New that also records c's Auth controller for tokenFor.
func newAPI(tb testing.TB, opts runtime.Options, log *slog.Logger, c *controllers.Controllers) (*runtime.API, error) {
	tb.Helper()
	a, err := api.New(opts, log, c)
	if err != nil {
		return nil, err
	}
	ac := c.Auth()
	apiTokens.Store(a, sync.OnceValues(func() (string, error) {
		pair, err := ac.Refresh(context.Background(), seedRefreshToken)
		return pair.AccessToken, err
	}))
	tb.Cleanup(func() { apiTokens.Delete(a) })
	return a, nil
}

// tokenFor is an access token of seedSession on a, made once per API by refreshing
// seedRefreshToken through the controller: no request, so no access log line and no rate limit
// budget. A test that refreshes seedRefreshToken through a's routes itself must not use it
// (the second refresh would be a reuse, which revokes the session).
func tokenFor(tb testing.TB, a *runtime.API) string {
	tb.Helper()
	f, ok := apiTokens.Load(a)
	require.True(tb, ok, "the API was not built by newAPI")
	token, err := f.(func() (string, error))()
	require.NoError(tb, err)
	return token
}

// signedIn is a's public handler signed in as seedUser: it adds tokenFor's token to every
// request that has no Authorization of its own.
func signedIn(tb testing.TB, a *runtime.API) http.Handler {
	tb.Helper()
	token := tokenFor(tb, a)
	h := a.Public()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		h.ServeHTTP(w, r)
	})
}
