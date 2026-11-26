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

// The signed-in account routes (stage 16f part 3) over seedUser (seed_auth_test.go).

// accessToken trades the seeded refresh token for an access token of seedSession. Call it once
// per API: the refresh rotates the token, so a second call is reuse and revokes the session.
func accessToken(t *testing.T, h http.Handler) string {
	t.Helper()
	return string(requireRefreshed(t, postJSON(t, h, route.AuthRefresh, refreshBody(seedRefreshToken))).AccessToken)
}

// authed sends method target with an access token and, when body is not "", a JSON body.
func authed(t *testing.T, h http.Handler, method, target, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if body != "" {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", runtime.ContentTypeJSON)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return testutil.Do(t, h, req)
}

// authedJSON requires a 200 from GET target and decodes it.
func authedJSON[T any](t *testing.T, h http.Handler, target, token string) T {
	t.Helper()
	w := authed(t, h, http.MethodGet, target, token, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	return decodeJSON[T](t, w)
}

// requireTokenRefused requires the 401 for a refused access token.
func requireTokenRefused(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	requireProblem(t, w, dto.TypeUnauthorized, runtime.DetailBadAccessToken.Message())
	assert.Equal(t, runtime.WWWAuthenticate+`, error="invalid_token"`, w.Header().Get("WWW-Authenticate"))
}

// loginWithRecoveryCode logs seedUser in a second time (a new session) without using a TOTP
// step, and returns its access token.
func loginWithRecoveryCode(t *testing.T, h http.Handler) string {
	t.Helper()
	w := postJSON(t, h, route.AuthLoginTOTP, recoveryBody(login(t, h, seedUserName), seedRecoveryCode))
	return string(requirePair(t, w).AccessToken)
}

func sessionPath(id string) string { return strings.Replace(route.AccountSession, "{id}", id, 1) }

func TestAccount(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	token := accessToken(t, h)

	acct := authedJSON[dto.Account](t, h, route.Account, token)
	assert.Equal(t, dto.Account{ID: seedUser, Username: seedUserName, CreatedAt: seedTime.Add(-time.Hour),
		PasswordChangedAt: seedTime.Add(-time.Hour), RecoveryCodesLeft: 1}, acct)
	sessions := authedJSON[dto.Items[dto.Session]](t, h, route.AccountSessions, token).Items
	require.Len(t, sessions, 1)
	assert.Equal(t, seedSession, sessions[0].ID)
	assert.True(t, sessions[0].Current, "the caller's own session")
	testutil.RequireParamProblem(t, authed(t, h, http.MethodGet, route.Account+"?page=1", token, ""), dto.ParamPage,
		"not taken here")

	// Logout ends the session: its access and refresh tokens stop working.
	w := authed(t, h, http.MethodPost, route.AuthLogout, token, "")
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	requireTokenRefused(t, authed(t, h, http.MethodGet, route.Account, token, ""))
	requireTokenRefused(t, authed(t, h, http.MethodPost, route.AuthLogout, token, ""))
	assert.Empty(t, getJSON[dto.Items[dto.Session]](t, a.Admin(), userPath(seedUser, "/sessions")).Items)
}

func TestAccountUnauthorized(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	token := accessToken(t, h)
	get := func(header ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, route.Account, nil)
		for _, v := range header {
			req.Header.Add("Authorization", v)
		}
		logs.Reset()
		return testutil.Do(t, h, req)
	}
	accessLine := func() map[string]any {
		t.Helper()
		return testutil.AccessLine(t, logs)
	}

	// No bearer credentials (none, or another scheme): no RFC 6750 error code.
	for _, header := range [][]string{nil, {"Basic Zml4dHVyZTpmaXh0dXJl"}, {"Bearerx " + token}} {
		w := get(header...)
		require.Equal(t, http.StatusUnauthorized, w.Code, header)
		requireProblem(t, w, dto.TypeUnauthorized, runtime.DetailNoAccessToken.Message())
		assert.Equal(t, runtime.WWWAuthenticate, w.Header().Get("WWW-Authenticate"))
		assert.Equal(t, "missing", accessLine()["auth"])
	}

	// A malformed header is 400 invalid_request.
	for name, header := range map[string][]string{
		"no token":  {"Bearer"},
		"blank":     {"Bearer   "},
		"two parts": {"Bearer " + token + " " + token},
		"bad char":  {"Bearer " + token + "!"},
		"inner =":   {"Bearer a=b"},
		"twice":     {"Bearer " + token, "Bearer " + token},
		"too long":  {"Bearer " + strings.Repeat("a", runtime.MaxAuthorizationBytes)},
	} {
		w := get(header...)
		require.Equal(t, http.StatusBadRequest, w.Code, name)
		requireProblem(t, w, dto.TypeBadRequest, runtime.DetailBadAuthorization.Message())
		assert.Equal(t, runtime.WWWAuthenticate+`, error="invalid_request"`, w.Header().Get("WWW-Authenticate"), name)
		assert.Equal(t, "malformed", accessLine()["auth"], name)
	}

	// A well-formed token that isn't a valid JWT: the reason is logged, never the token.
	requireTokenRefused(t, get("Bearer fixture.not.ajwt=="))
	line := accessLine()
	assert.Equal(t, "invalid", line["auth"])
	assert.Equal(t, "malformed", line["token_reason"])
	assert.NotContains(t, logs.String(), "fixture.not.ajwt")

	// The scheme is case-insensitive and may be followed by several spaces.
	for _, header := range []string{"bearer " + token, "BEARER   " + token} {
		w := get(header)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		line := accessLine()
		assert.Equal(t, "ok", line["auth"])
		assert.EqualValues(t, seedUser, line["user_id"])
		assert.EqualValues(t, seedSession, line["session_id"])
		assert.NotContains(t, logs.String(), token)
	}

	// The admin socket never reads Authorization.
	w := authed(t, a.Admin(), http.MethodGet, route.Account, token, "")
	require.Equal(t, http.StatusUnauthorized, w.Code)
	requireProblem(t, w, dto.TypeUnauthorized, runtime.DetailAdminNoAccount.Message())
	assert.Equal(t, runtime.WWWAuthenticate, w.Header().Get("WWW-Authenticate"))

	// Disabling the account stops its tokens at once.
	require.Equal(t, http.StatusNoContent, postJSON(t, a.Admin(), userPath(seedUser, "/disable"), "").Code)
	requireTokenRefused(t, get("Bearer "+token))
}

func TestChangePassword(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	token := accessToken(t, h)
	other := loginWithRecoveryCode(t, h)
	put := func(body string) *httptest.ResponseRecorder {
		return authed(t, h, http.MethodPut, route.AccountPassword, token, body)
	}

	assert.Equal(t, []string{"/code", "/current_password", "/password"}, keys(problemPointers(t, put(`{}`), "empty")))
	w := put(changePasswordBody(fixturePassword, "short", seedTOTPCode))
	assert.Equal(t, map[string]string{"/password": runtime.DetailPasswordTooShort.Message()}, problemPointers(t, w, "policy"))

	const next = "fixture battery staple"
	w = put(changePasswordBody(fixturePassword, next, seedTOTPCode))
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

	// Every other session is revoked; this one stays signed in.
	requireTokenRefused(t, authed(t, h, http.MethodGet, route.Account, other, ""))
	acct := authedJSON[dto.Account](t, h, route.Account, token)
	assert.Equal(t, seedTime, acct.PasswordChangedAt)
	w = postJSON(t, h, route.AuthLogin, loginBody(seedUserName, next))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func changePasswordBody(current, next, code string) string {
	return `{"current_password":"` + current + `","password":"` + next + `","code":"` + code + `"}`
}

// A wrong current password or code is 403 (the token was fine) and backs the account off.
func TestChangePasswordRefused(t *testing.T) {
	for name, body := range map[string]string{
		"wrong password": changePasswordBody("fixture wrong horse", "fixture battery staple", seedTOTPCode),
		"wrong code":     changePasswordBody(fixturePassword, "fixture battery staple", wrongCode(seedTOTPCode)),
	} {
		t.Run(name, func(t *testing.T) {
			a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
			h := a.Public()
			token := accessToken(t, h)
			w := authed(t, h, http.MethodPut, route.AccountPassword, token, body)
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			requireProblem(t, w, dto.TypeForbidden, runtime.DetailReauthCredentials.Message())
			assert.Empty(t, w.Header().Get("WWW-Authenticate"))

			// The clock doesn't move, so even the right credentials are now backed off.
			w = authed(t, h, http.MethodPut, route.AccountPassword, token,
				changePasswordBody(fixturePassword, "fixture battery staple", seedTOTPCode))
			require.Equal(t, http.StatusTooManyRequests, w.Code, w.Body.String())
			requireProblem(t, w, dto.TypeRateLimited, runtime.DetailLoginBackoff.Message())
			assert.Equal(t, "1", w.Header().Get("Retry-After"))
		})
	}
}

func TestRegenerateRecoveryCodes(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	token := accessToken(t, h)
	post := func(body string) *httptest.ResponseRecorder {
		return authed(t, h, http.MethodPost, route.AccountRecoveryCodes, token, body)
	}

	assert.Equal(t, []string{"/code"}, keys(problemPointers(t, post(`{}`), "empty")))
	w := post(`{"code":"` + seedTOTPCode + `"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	codes := decodeJSON[dto.RecoveryCodes](t, w).RecoveryCodes
	assert.Len(t, codes, 10)
	assert.Regexp(t, `^[a-z2-7]{4}(-[a-z2-7]{4}){5}$`, string(codes[0]), "verbatim, not hashed")
	assert.Equal(t, 10, authedJSON[dto.Account](t, h, route.Account, token).RecoveryCodesLeft)

	// The same TOTP step again is a replay: 403.
	w = post(`{"code":"` + seedTOTPCode + `"}`)
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	requireProblem(t, w, dto.TypeForbidden, runtime.DetailReauthCredentials.Message())
}

// New recovery codes replace the old ones (a separate API: a failure above backs the account off).
func TestRegeneratedRecoveryCodesLogIn(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	w := authed(t, h, http.MethodPost, route.AccountRecoveryCodes, accessToken(t, h), `{"code":"`+seedTOTPCode+`"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	codes := decodeJSON[dto.RecoveryCodes](t, w).RecoveryCodes
	requirePair(t, postJSON(t, h, route.AuthLoginTOTP, recoveryBody(login(t, h, seedUserName), string(codes[0]))))
	w = postJSON(t, h, route.AuthLoginTOTP, recoveryBody(seedChallengeToken, seedRecoveryCode))
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidCredentials.Message())
}

func TestRevokeAccountSession(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	token := accessToken(t, h)
	second := loginWithRecoveryCode(t, h) // session 2

	// seedOtherUser, enabled, logs in: session 3 is another account's.
	require.Equal(t, http.StatusNoContent, postJSON(t, a.Admin(), userPath(seedOtherUser, "/enable"), "").Code)
	theirs := string(requirePair(t, postJSON(t, h, route.AuthLoginTOTP,
		totpBody(login(t, h, seedOtherName), seedTOTPCode))).AccessToken)
	ids := func(tok string) []int64 {
		var out []int64
		for _, s := range authedJSON[dto.Items[dto.Session]](t, h, route.AccountSessions, tok).Items {
			out = append(out, s.ID)
		}
		return out
	}
	assert.Equal(t, []int64{2, 1}, ids(token), "newest first")
	assert.Equal(t, []int64{3}, ids(theirs))
	current := func(tok string) []bool {
		var out []bool
		for _, s := range authedJSON[dto.Items[dto.Session]](t, h, route.AccountSessions, tok).Items {
			out = append(out, s.Current)
		}
		return out
	}
	assert.Equal(t, []bool{false, true}, current(token), "session 1 is the caller's")
	assert.Equal(t, []bool{true, false}, current(second), "session 2 is the caller's")

	// Another account's session is 404, like a missing one, and is left alone (OWASP API1).
	for _, id := range []string{"3", "999"} {
		w := authed(t, h, http.MethodDelete, sessionPath(id), token, "")
		require.Equal(t, http.StatusNotFound, w.Code, id)
		requireProblem(t, w, dto.TypeNotFound, runtime.DetailNoSession.Message())
	}
	assert.Equal(t, []int64{3}, ids(theirs))
	testutil.RequireParamProblem(t, authed(t, h, http.MethodDelete, sessionPath("x"), token, ""), dto.ParamID, "not an id")

	w := authed(t, h, http.MethodDelete, sessionPath("2"), token, "")
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	requireTokenRefused(t, authed(t, h, http.MethodGet, route.Account, second, ""))
	w = authed(t, h, http.MethodDelete, sessionPath("2"), token, "")
	require.Equal(t, http.StatusNoContent, w.Code, "revoking a revoked session is a no-op")
	assert.Equal(t, []int64{1}, ids(token))
}
