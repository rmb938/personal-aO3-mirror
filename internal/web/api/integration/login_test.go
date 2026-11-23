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
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

// The login and refresh routes (stage 16f part 2) over seedUser (seed_auth_test.go). The clock
// is fixed at seedTime, so a TOTP code works once per step: later logins use the next step's.

func loginBody(username, password string) string {
	return `{"username":"` + username + `","password":"` + password + `"}`
}

func totpBody(challenge, code string) string {
	return `{"challenge_token":"` + challenge + `","code":"` + code + `"}`
}

func recoveryBody(challenge, code string) string {
	return `{"challenge_token":"` + challenge + `","recovery_code":"` + code + `"}`
}

func refreshBody(token string) string { return `{"refresh_token":"` + token + `"}` }

// login runs the password step and returns the challenge token.
func login(t *testing.T, h http.Handler, username string) string {
	t.Helper()
	w := postJSON(t, h, route.AuthLogin, loginBody(username, fixturePassword))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	c := decodeJSON[dto.LoginChallenge](t, w)
	assert.True(t, strings.HasPrefix(string(c.ChallengeToken), "chl_"), "verbatim, not hashed")
	assert.Equal(t, seedTime.Add(5*time.Minute), c.ExpiresAt)
	return string(c.ChallengeToken)
}

// requirePair requires a 200 token pair from a login (a new session, ending a refresh TTL from
// now) and returns it.
func requirePair(t *testing.T, w *httptest.ResponseRecorder) dto.TokenPair {
	t.Helper()
	return requirePairUntil(t, w, seedTime.Add(720*time.Hour))
}

// requireRefreshed requires a 200 token pair from refreshing seedSession, which was created a
// minute before seedTime: the default auth.max_session_age (720h, equal to the refresh TTL)
// caps it at its login plus 720h.
func requireRefreshed(t *testing.T, w *httptest.ResponseRecorder) dto.TokenPair {
	t.Helper()
	return requirePairUntil(t, w, seedTime.Add(-time.Minute).Add(720*time.Hour))
}

// requirePairUntil requires a 200 token pair whose refresh token expires at refreshExpires.
func requirePairUntil(t *testing.T, w *httptest.ResponseRecorder, refreshExpires time.Time) dto.TokenPair {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	p := decodeJSON[dto.TokenPair](t, w)
	assert.Equal(t, dto.TokenTypeBearer, p.TokenType)
	assert.Equal(t, int64(15*60), p.ExpiresIn)
	assert.True(t, strings.HasPrefix(string(p.AccessToken), "eyJ"), "verbatim, not hashed")
	assert.True(t, strings.HasPrefix(string(p.RefreshToken), "rt_"), "verbatim, not hashed")
	assert.Equal(t, refreshExpires, p.RefreshExpiresAt)
	return p
}

func TestLogin(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()

	// The username is case-folded.
	challenge := login(t, h, "Fixture_User")
	first := requirePair(t, postJSON(t, h, route.AuthLoginTOTP, totpBody(challenge, seedTOTPCode)))
	w := postJSON(t, h, route.AuthLoginTOTP, totpBody(challenge, seedTOTPCode))
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidToken.Message())

	// The seeded challenge with the recovery code, typed loosely; it works once.
	recovered := requirePair(t, postJSON(t, h, route.AuthLoginTOTP, recoveryBody(seedChallengeToken, "FIXT UREC-OVRY CODE")))
	u, _ := findUser(t, a.Admin(), seedUser)
	assert.Equal(t, 0, u.RecoveryCodesLeft)
	w = postJSON(t, h, route.AuthLoginTOTP, recoveryBody(login(t, h, seedUserName), seedRecoveryCode))
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidCredentials.Message())

	sessions := getJSON[dto.Items[dto.Session]](t, a.Admin(), userPath(seedUser, "/sessions")).Items
	assert.Len(t, sessions, 3, "the seeded session and two logins")
	assert.NotEqual(t, first.RefreshToken, recovered.RefreshToken)
}

func TestRefresh(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()

	next := requireRefreshed(t, postJSON(t, h, route.AuthRefresh, refreshBody(seedRefreshToken)))
	assert.NotEqual(t, seedRefreshToken, string(next.RefreshToken), "rotated")
	again := requireRefreshed(t, postJSON(t, h, route.AuthRefresh, refreshBody(string(next.RefreshToken))))

	// The previous token again is reuse: the session is revoked, so the current one fails too.
	w := postJSON(t, h, route.AuthRefresh, refreshBody(string(next.RefreshToken)))
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidToken.Message())
	w = postJSON(t, h, route.AuthRefresh, refreshBody(string(again.RefreshToken)))
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidToken.Message())
	assert.Empty(t, getJSON[dto.Items[dto.Session]](t, a.Admin(), userPath(seedUser, "/sessions")).Items)

	w = postJSON(t, h, route.AuthRefresh, refreshBody(fixtureToken("rt_", "unknown")))
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidToken.Message())
	assert.Equal(t, []string{"/refresh_token"}, keys(problemPointers(t, postJSON(t, h, route.AuthRefresh, `{}`), "empty")))
}

func TestLoginRefused(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()

	assert.Equal(t, []string{"/password", "/username"}, keys(problemPointers(t, postJSON(t, h, route.AuthLogin, `{}`), "empty")))
	w := postJSON(t, h, route.AuthLogin, loginBody("fixture.user", fixturePassword))
	assert.Equal(t, map[string]string{"/username": request.DetailUsername.Message()}, problemPointers(t, w, "username"))

	// An unknown user, a wrong password and a disabled account look the same; each failure
	// then backs that name off (the clock doesn't move, so the next try is refused).
	for name, body := range map[string]string{
		"unknown":        loginBody("fixture_nobody", fixturePassword),
		"wrong password": loginBody(seedUserName, "fixture wrong horse"),
		"disabled":       loginBody(seedOtherName, fixturePassword),
	} {
		w := postJSON(t, h, route.AuthLogin, body)
		require.Equal(t, http.StatusUnauthorized, w.Code, name)
		requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidCredentials.Message())
		w = postJSON(t, h, route.AuthLogin, body)
		require.Equal(t, http.StatusTooManyRequests, w.Code, "%s: %s", name, w.Body.String())
		requireProblem(t, w, dto.TypeRateLimited, runtime.DetailLoginBackoff.Message())
		assert.Equal(t, "1", w.Header().Get("Retry-After"), name)
	}
}

func TestLoginSecondFactorRefused(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()

	for body, want := range map[string][]string{
		`{}`: {"/challenge_token", "/code"},
		`{"challenge_token":"` + seedChallengeToken + `","code":"123456","recovery_code":"` + seedRecoveryCode + `"}`: {"/recovery_code"},
		totpBody(seedChallengeToken, "12345"):         {"/code"},
		recoveryBody(seedChallengeToken, "fixt-urec"): {"/recovery_code"},
	} {
		assert.Equal(t, want, keys(problemPointers(t, postJSON(t, h, route.AuthLoginTOTP, body), body)), body)
	}
	w := postJSON(t, h, route.AuthLoginTOTP, totpBody(fixtureToken("chl_", "unknown"), seedTOTPCode))
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidToken.Message())

	// A wrong code counts against the account: the right one is then backed off.
	w = postJSON(t, h, route.AuthLoginTOTP, totpBody(seedChallengeToken, wrongCode(seedTOTPCode)))
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidCredentials.Message())
	w = postJSON(t, h, route.AuthLoginTOTP, totpBody(seedChallengeToken, seedTOTPCode))
	requireProblem(t, w, dto.TypeRateLimited, runtime.DetailLoginBackoff.Message())
	assert.Equal(t, "1", w.Header().Get("Retry-After"))
}

// Each login step has its own budget of api.rate.login_per_min.
func TestLoginStepBudgets(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, func(o *runtime.Options) { o.LoginPerMin = 1 })
	h := a.Public()
	assert.Equal(t, http.StatusUnprocessableEntity, postJSON(t, h, route.AuthLogin, `{}`).Code)
	assert.Equal(t, http.StatusTooManyRequests, postJSON(t, h, route.AuthLogin, `{}`).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, postJSON(t, h, route.AuthLoginTOTP, `{}`).Code)
}

// The socket serves login and refresh too.
func TestLoginOnAdminSocket(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	requirePair(t, postJSON(t, a.Admin(), route.AuthLoginTOTP, totpBody(login(t, a.Admin(), seedUserName), seedTOTPCode)))
	requireRefreshed(t, postJSON(t, a.Admin(), route.AuthRefresh, refreshBody(seedRefreshToken)))
}
