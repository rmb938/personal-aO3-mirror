package request

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

var (
	fixtureInviteToken     = "inv_" + strings.Repeat("A", 42) + "w"
	fixtureEnrollmentToken = "enr_" + strings.Repeat("_", 42) + "-"
)

func TestSignupValid(t *testing.T) {
	for _, s := range []Signup{
		{InviteToken: fixtureInviteToken, Username: "abc", Password: "x"},
		{InviteToken: fixtureInviteToken, Username: "Fixture_User-01", Password: strings.Repeat("é", MaxPasswordBytes/2)},
		{InviteToken: fixtureInviteToken, Username: strings.Repeat("z", MaxUsernameLen), Password: "fixture password"},
	} {
		assert.Empty(t, errorsOf(t, &s), "%+v", s)
	}
}

func TestSignupInvalid(t *testing.T) {
	want := map[string]string{
		"/invite_token": detailInviteToken.Message(),
		"/username":     DetailUsername.Message(),
		"/password":     detailPassword.Message(),
	}
	assert.Equal(t, want, errorsOf(t, &Signup{}))
	for _, s := range []Signup{
		{InviteToken: fixtureEnrollmentToken, Username: "ab", Password: strings.Repeat("x", MaxPasswordBytes+1)},
		{InviteToken: fixtureInviteToken[:len(fixtureInviteToken)-1], Username: strings.Repeat("z", MaxUsernameLen+1)},
		{InviteToken: fixtureInviteToken + "A", Username: "fixture user"},
		{InviteToken: "inv_" + strings.Repeat("A", 42) + "=", Username: "fixture.user"},
		{InviteToken: "INV_" + strings.Repeat("A", 43), Username: "fixtüre"},
	} {
		assert.Equal(t, want, errorsOf(t, &s), "%+v", s)
	}
}

func TestResetValidation(t *testing.T) {
	for _, s := range []Reset{
		{InviteToken: fixtureInviteToken},
		{InviteToken: fixtureInviteToken, Password: strings.Repeat("x", MaxPasswordBytes)},
	} {
		assert.Empty(t, errorsOf(t, &s), "%+v", s)
	}
	assert.Equal(t, map[string]string{
		"/invite_token": detailInviteToken.Message(),
		"/password":     detailResetPassword.Message(),
	}, errorsOf(t, &Reset{InviteToken: fixtureEnrollmentToken, Password: strings.Repeat("x", MaxPasswordBytes+1)}))
}

func TestConfirmEnrollmentValidation(t *testing.T) {
	for _, code := range []string{"000000", "123456", "999999"} {
		c := ConfirmEnrollment{EnrollmentToken: fixtureEnrollmentToken, Code: code}
		assert.Empty(t, errorsOf(t, &c), code)
	}
	want := map[string]string{
		"/enrollment_token": detailEnrollmentToken.Message(),
		"/code":             detailTOTPCode.Message(),
	}
	for _, c := range []ConfirmEnrollment{
		{},
		{EnrollmentToken: fixtureInviteToken, Code: "12345"},
		{EnrollmentToken: "enr_", Code: "1234567"},
		{EnrollmentToken: fixtureEnrollmentToken + " ", Code: "12 456"},
		{EnrollmentToken: " " + fixtureEnrollmentToken, Code: "١٢٣٤٥٦"},
	} {
		assert.Equal(t, want, errorsOf(t, &c), "%+v", c)
	}
}

var (
	fixtureChallengeToken = "chl_" + strings.Repeat("B", 43)
	fixtureRefreshToken   = "rt_" + strings.Repeat("c", 43)
)

func TestLoginValidation(t *testing.T) {
	for _, s := range []Login{
		{Username: "Fixture_User", Password: "x"},
		{Username: strings.Repeat("z", MaxUsernameLen), Password: strings.Repeat("x", MaxPasswordBytes)},
	} {
		assert.Empty(t, errorsOf(t, &s), "%+v", s)
	}
	want := map[string]string{"/username": DetailUsername.Message(), "/password": detailPassword.Message()}
	for _, s := range []Login{{}, {Username: "fixture.user", Password: strings.Repeat("x", MaxPasswordBytes+1)}} {
		assert.Equal(t, want, errorsOf(t, &s), "%+v", s)
	}
}

func TestLoginTOTPValidation(t *testing.T) {
	for _, s := range []LoginTOTP{
		{ChallengeToken: fixtureChallengeToken, Code: "123456"},
		{ChallengeToken: fixtureChallengeToken, RecoveryCode: "abcd-efgh-ijkl-mnop-qrst-uv23"},
		{ChallengeToken: fixtureChallengeToken, RecoveryCode: "ABCDEFGHIJKLMNOPQRSTUV67"},
		{ChallengeToken: fixtureChallengeToken, RecoveryCode: " abcd efgh-ijkl mnop-qrst uv23 "},
		{ChallengeToken: fixtureChallengeToken, RecoveryCode: "abcdefghijklmnopqrstuv23" + strings.Repeat("-", 72)},
		{ChallengeToken: fixtureChallengeToken, Code: "123456", Transport: dto.TransportBearer},
		{ChallengeToken: fixtureChallengeToken, Code: "123456", Transport: dto.TransportCookie},
	} {
		assert.Empty(t, errorsOf(t, &s), "%+v", s)
	}
	for _, c := range []struct {
		s    LoginTOTP
		want map[string]string
	}{
		{LoginTOTP{}, map[string]string{"/challenge_token": detailChallenge.Message(), "/code": detailSecondFactor.Message()}},
		{LoginTOTP{ChallengeToken: fixtureEnrollmentToken, Code: "123456", RecoveryCode: "abcd-efgh-ijkl-mnop-qrst-uv23"},
			map[string]string{"/challenge_token": detailChallenge.Message(), "/recovery_code": detailSecondFactor.Message()}},
		{LoginTOTP{ChallengeToken: fixtureChallengeToken, Code: "12345"}, map[string]string{"/code": detailTOTPCode.Message()}},
		{LoginTOTP{ChallengeToken: fixtureChallengeToken, Code: "123456", Transport: "cookies"},
			map[string]string{"/transport": detailTransport.Message()}},
		{LoginTOTP{ChallengeToken: fixtureChallengeToken, RecoveryCode: "abcd-efgh-ijkl-mnop-qrst-uv2"},
			map[string]string{"/recovery_code": detailRecoveryCode.Message()}},
		{LoginTOTP{ChallengeToken: fixtureChallengeToken, RecoveryCode: "abcd-efgh-ijkl-mn23"}, // the old 16-character form
			map[string]string{"/recovery_code": detailRecoveryCode.Message()}},
		{LoginTOTP{ChallengeToken: fixtureChallengeToken, RecoveryCode: "abcd-efgh-ijkl-mnop-qrst-uv01"}, // 0 and 1 aren't base32
			map[string]string{"/recovery_code": detailRecoveryCode.Message()}},
		{LoginTOTP{ChallengeToken: fixtureChallengeToken, RecoveryCode: "abcdefghijklmnopqrstuv23" + strings.Repeat("-", 73)},
			map[string]string{"/recovery_code": detailRecoveryCode.Message()}},
	} {
		assert.Equal(t, c.want, errorsOf(t, &c.s), "%+v", c.s)
	}
}

func TestRefreshValidation(t *testing.T) {
	for _, s := range []Refresh{
		{RefreshToken: fixtureRefreshToken},
		{RefreshToken: fixtureRefreshToken, Transport: dto.TransportBearer},
		{Transport: dto.TransportCookie},
	} {
		assert.Empty(t, errorsOf(t, &s), "%+v", s)
	}
	want := map[string]string{"/refresh_token": detailRefreshToken.Message()}
	for _, s := range []Refresh{{}, {RefreshToken: fixtureChallengeToken}, {RefreshToken: fixtureRefreshToken + "A"},
		{Transport: dto.TransportBearer}} {
		assert.Equal(t, want, errorsOf(t, &s), "%+v", s)
	}
	assert.Equal(t, map[string]string{"/refresh_token": detailCookieRefresh.Message()},
		errorsOf(t, &Refresh{RefreshToken: fixtureRefreshToken, Transport: dto.TransportCookie}))
	assert.Equal(t, map[string]string{"/transport": detailTransport.Message(), "/refresh_token": detailRefreshToken.Message()},
		errorsOf(t, &Refresh{Transport: "Cookie"}), "every invalid member at once")
}

func TestChangePasswordValidation(t *testing.T) {
	ok := ChangePassword{CurrentPassword: "x", Password: strings.Repeat("x", MaxPasswordBytes), Code: "012345"}
	assert.Empty(t, errorsOf(t, &ok))
	want := map[string]string{
		"/current_password": detailPassword.Message(),
		"/password":         detailPassword.Message(),
		"/code":             detailTOTPCode.Message(),
	}
	assert.Equal(t, want, errorsOf(t, &ChangePassword{}))
	assert.Equal(t, want, errorsOf(t, &ChangePassword{CurrentPassword: strings.Repeat("x", MaxPasswordBytes+1),
		Password: strings.Repeat("x", MaxPasswordBytes+1), Code: "12345a"}))
}

func TestRegenerateRecoveryCodesValidation(t *testing.T) {
	assert.Empty(t, errorsOf(t, &RegenerateRecoveryCodes{Code: "999999"}))
	for _, code := range []string{"", "12345", "1234567", "12 456"} {
		assert.Equal(t, map[string]string{"/code": detailTOTPCode.Message()},
			errorsOf(t, &RegenerateRecoveryCodes{Code: code}), code)
	}
}
