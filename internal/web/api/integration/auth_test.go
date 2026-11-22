package integration

import (
	"encoding/base32"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

// The public auth entry points (stage 16f) over the seeded invites and enrollments
// (seed_auth_test.go). The clock is fixed at seedTime.

const fixturePassword = "fixture correct horse"

func signupBody(invite, username, password string) string {
	return `{"invite_token":"` + invite + `","username":"` + username + `","password":"` + password + `"}`
}

func confirmBody(enrollment, code string) string {
	return `{"enrollment_token":"` + enrollment + `","code":"` + code + `"}`
}

// totpCode is the code at t for an Enrollment's base32 secret.
func totpCode(t *testing.T, secret dto.TOTPSecret, at time.Time) string {
	t.Helper()
	b, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(string(secret))
	require.NoError(t, err)
	return auth.TOTPCode(b, at)
}

// requireProblem requires a problem of typ with detail d; a 401 also names the bearer scheme
// (with no RFC 6750 error code: the account routes' tests check those themselves).
func requireProblem(t *testing.T, w *httptest.ResponseRecorder, typ dto.ProblemType, d string) {
	t.Helper()
	p := testutil.DecodeProblem(t, w)
	require.Equal(t, typ, p.Type, w.Body.String())
	assert.Equal(t, d, p.Detail)
	if w.Code == http.StatusUnauthorized && typ != dto.TypeUnauthorized {
		assert.Equal(t, runtime.WWWAuthenticate, w.Header().Get("WWW-Authenticate"))
	}
}

// inviteToken is the token in an invite link's fragment.
func inviteToken(t *testing.T, l dto.InviteLink) string {
	t.Helper()
	_, tok, ok := strings.Cut(string(l.URL), "#token=")
	require.True(t, ok, "invite link has a token")
	return tok
}

func TestSignup(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()

	w := postJSON(t, h, route.AuthSignup, signupBody(seedInviteToken, "Fixture_Signup", fixturePassword))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	e := decodeJSON[dto.Enrollment](t, w)
	assert.True(t, strings.HasPrefix(string(e.EnrollmentToken), "enr_"), "verbatim, not hashed")
	assert.Contains(t, string(e.OTPAuthURI), ":fixture_signup?secret="+string(e.TOTPSecret)+"&", "lower-cased name")
	assert.Equal(t, seedTime.Add(15*time.Minute), e.ExpiresAt)

	// The new signup replaced the seeded one on the same invite.
	w = postJSON(t, h, route.AuthSignupConfirm, confirmBody(seedSignupEnrollToken, seedTOTPCode))
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidToken.Message())
	w = postJSON(t, h, route.AuthSignupConfirm, confirmBody(string(e.EnrollmentToken), wrongCode(totpCode(t, e.TOTPSecret, seedTime))))
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidCredentials.Message())

	w = postJSON(t, h, route.AuthSignupConfirm, confirmBody(string(e.EnrollmentToken), totpCode(t, e.TOTPSecret, seedTime)))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	codes := decodeJSON[dto.RecoveryCodes](t, w).RecoveryCodes
	assert.Len(t, codes, auth.RecoveryCodeCount)
	u, ok := findUser(t, a.Admin(), 3)
	require.True(t, ok, "account created")
	assert.Equal(t, dto.Username("fixture_signup"), u.Username)
	assert.Equal(t, auth.RecoveryCodeCount, u.RecoveryCodesLeft)

	// The invite and the enrollment are spent.
	w = postJSON(t, h, route.AuthSignupConfirm, confirmBody(string(e.EnrollmentToken), totpCode(t, e.TOTPSecret, seedTime)))
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidToken.Message())
	w = postJSON(t, h, route.AuthSignup, signupBody(seedInviteToken, "fixture_again", fixturePassword))
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidToken.Message())
	w = postJSON(t, h, route.AuthSignup, signupBody(fixtureToken(auth.InviteToken, "unknown"), "fixture_again", fixturePassword))
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidToken.Message())
}

// wrongCode is a 6-digit code other than code.
func wrongCode(code string) string {
	if code == "000000" {
		return "000001"
	}
	return "000000"
}

func TestSignupRefused(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()

	// Every malformed member at once; the controller's checks come after.
	assert.Equal(t, []string{"/invite_token", "/password", "/username"},
		keys(problemPointers(t, postJSON(t, h, route.AuthSignup, `{}`), "empty")))
	w := postJSON(t, h, route.AuthSignup, signupBody(seedInviteToken, "fixture.user", fixturePassword))
	assert.Equal(t, map[string]string{"/username": request.DetailUsername.Message()}, problemPointers(t, w, "username"))

	for password, d := range map[string]string{
		"short":                  runtime.DetailPasswordTooShort.Message(),
		"Fixture_User_X":         runtime.DetailPasswordIsUsername.Message(),
		"PassWord":               runtime.DetailPasswordCommon.Message(),
		"fixture\\tpass":         runtime.DetailPasswordInvalid.Message(),
		strings.Repeat("x", 129): runtime.DetailPasswordTooLong.Message(),
	} {
		w := postJSON(t, h, route.AuthSignup, signupBody(seedInviteToken, "fixture_user_x", password))
		assert.Equal(t, map[string]string{"/password": d}, problemPointers(t, w, password))
	}

	w = postJSON(t, h, route.AuthSignup, signupBody(seedInviteToken, "FIXTURE_USER", fixturePassword))
	require.Equal(t, http.StatusConflict, w.Code)
	requireProblem(t, w, dto.TypeAlreadyExists, runtime.DetailUsernameTaken.Message())
	assert.Empty(t, w.Header().Get("Location"))

	// A username taken between the two steps: the seeded signup is for seedNewName.
	l := requireInviteLink(t, postJSON(t, a.Admin(), route.AdminInvites, `{}`), "signup", 24*time.Hour)
	w = postJSON(t, h, route.AuthSignup, signupBody(inviteToken(t, l), seedNewName, fixturePassword))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	e := decodeJSON[dto.Enrollment](t, w)
	w = postJSON(t, h, route.AuthSignupConfirm, confirmBody(string(e.EnrollmentToken), totpCode(t, e.TOTPSecret, seedTime)))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	w = postJSON(t, h, route.AuthSignupConfirm, confirmBody(seedSignupEnrollToken, seedTOTPCode))
	requireProblem(t, w, dto.TypeAlreadyExists, runtime.DetailUsernameTaken.Message())
}

func TestReset(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()

	// The seeded reset replaces seedUser's TOTP secret: no password.
	w := postJSON(t, h, route.AuthReset, `{"invite_token":"`+seedResetToken+`","password":"`+fixturePassword+`"}`)
	assert.Equal(t, map[string]string{"/password": runtime.DetailResetPassword.Message()}, problemPointers(t, w, "password"))
	w = postJSON(t, h, route.AuthReset, `{"invite_token":"`+seedResetToken+`"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	e := decodeJSON[dto.Enrollment](t, w)
	assert.Contains(t, string(e.OTPAuthURI), ":"+seedUserName+"?secret=")

	w = postJSON(t, h, route.AuthResetConfirm, confirmBody(string(e.EnrollmentToken), wrongCode(totpCode(t, e.TOTPSecret, seedTime))))
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidCredentials.Message())
	w = postJSON(t, h, route.AuthResetConfirm, confirmBody(string(e.EnrollmentToken), totpCode(t, e.TOTPSecret, seedTime)))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Len(t, decodeJSON[dto.RecoveryCodes](t, w).RecoveryCodes, auth.RecoveryCodeCount, "new secret, new codes")
	assert.Empty(t, getJSON[dto.Items[dto.Session]](t, a.Admin(), userPath(seedUser, "/sessions")).Items, "sessions revoked")
	w = postJSON(t, h, route.AuthReset, `{"invite_token":"`+seedResetToken+`"}`)
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidToken.Message())

	// A password-only reset keeps the (new) secret: confirm with its next code (this step's was
	// just used) and keep the recovery codes.
	l := requireInviteLink(t, postJSON(t, a.Admin(), userPath(seedUser, "/reset"), `{"scope":"password"}`), "reset", 24*time.Hour)
	w = postJSON(t, h, route.AuthReset, `{"invite_token":"`+inviteToken(t, l)+`"}`)
	assert.Equal(t, map[string]string{"/password": runtime.DetailResetPassword.Message()}, problemPointers(t, w, "no password"))
	w = postJSON(t, h, route.AuthReset, `{"invite_token":"`+inviteToken(t, l)+`","password":"`+fixturePassword+`"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), "totp_secret")
	p := decodeJSON[dto.Enrollment](t, w)
	w = postJSON(t, h, route.AuthResetConfirm, confirmBody(string(p.EnrollmentToken), totpCode(t, e.TOTPSecret, seedTime)))
	requireProblem(t, w, dto.TypeInvalidCredentials, runtime.DetailInvalidCredentials.Message())
	w = postJSON(t, h, route.AuthResetConfirm, confirmBody(string(p.EnrollmentToken), totpCode(t, e.TOTPSecret, seedTime.Add(auth.TOTPPeriod))))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{"recovery_codes":[]}`, w.Body.String())
	u, _ := findUser(t, a.Admin(), seedUser)
	assert.Equal(t, seedTime, u.PasswordChangedAt)
	assert.Equal(t, auth.RecoveryCodeCount, u.RecoveryCodesLeft)
}

// The socket serves the entry points too (an admin can sign up from the host).
func TestAuthOnAdminSocket(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	w := postJSON(t, a.Admin(), route.AuthSignupConfirm, confirmBody(seedSignupEnrollToken, seedTOTPCode))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
}
