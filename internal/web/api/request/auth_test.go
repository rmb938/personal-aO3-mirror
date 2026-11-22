package request

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
