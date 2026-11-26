package controllers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// requireUserHash checks that user uid's stored password hash was re-wrapped for the user on
// confirm: it verifies under the user's context and not under its enrollment's (invite
// inviteID).
func requireUserHash(t *testing.T, e *authEnv, uid, inviteID int64, password string) {
	t.Helper()
	u, err := e.users.AutoCommitForTestOnly().UserByID(context.Background(), uid)
	require.NoError(t, err)
	ok, err := e.opts.Hasher.Verify(context.Background(), auth.UserPassword(uid), u.PasswordPHC, password)
	require.NoError(t, err)
	require.True(t, ok)
	_, err = e.opts.Hasher.Verify(context.Background(), auth.EnrollmentPassword(inviteID), u.PasswordPHC, password)
	require.ErrorIs(t, err, auth.ErrDecrypt)
}

func TestAuthInviteSingleUseAndExpiry(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	l, err := e.a.CreateInvite(ctx, time.Hour)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(l.URL, "https://mirror.example.com/signup#token=inv_"))
	assert.Equal(t, e.clk.Now().Add(time.Hour), l.ExpiresAt)
	tok := inviteToken(t, l)

	en, err := e.a.Signup(ctx, tok, "fixture_a", testPassword)
	require.NoError(t, err)
	assert.Equal(t, e.clk.Now().Add(EnrollmentTTL), en.ExpiresAt)
	assert.True(t, strings.HasPrefix(en.OTPAuthURI, "otpauth://totp/ao3mirror:fixture_a?secret="+en.TOTPSecret))
	c, err := e.a.ConfirmSignup(ctx, en.Token, auth.TOTPCode(decodeSecret(t, en.TOTPSecret), e.clk.Now()))
	require.NoError(t, err)
	requireUserHash(t, e, c.UserID, l.ID, testPassword)

	_, err = e.a.Signup(ctx, tok, "fixture_b", testPassword)
	require.ErrorIs(t, err, ErrInvalidToken, "used")
	_, err = e.a.StartReset(ctx, tok, "")
	require.ErrorIs(t, err, ErrInvalidToken, "a signup invite is not a reset")

	l, err = e.a.CreateInvite(ctx, time.Hour)
	require.NoError(t, err)
	e.advance(time.Hour)
	_, err = e.a.Signup(ctx, inviteToken(t, l), "fixture_b", testPassword)
	require.ErrorIs(t, err, ErrInvalidToken, "expired")

	l, err = e.a.CreateInvite(ctx, 0)
	require.NoError(t, err)
	assert.Equal(t, e.clk.Now().Add(24*time.Hour), l.ExpiresAt, "default TTL")
	require.NoError(t, e.a.RevokeInvite(ctx, l.ID))
	_, err = e.a.Signup(ctx, inviteToken(t, l), "fixture_b", testPassword)
	require.ErrorIs(t, err, ErrInvalidToken, "revoked")

	for _, bad := range []string{"", "inv_x", "enr_" + inviteToken(t, l)[4:]} {
		_, err = e.a.Signup(ctx, bad, "fixture_b", testPassword)
		require.ErrorIs(t, err, ErrInvalidToken, bad)
	}
	for _, ttl := range []time.Duration{-time.Hour, time.Second, MaxInviteTTL + time.Second} {
		_, err = e.a.CreateInvite(ctx, ttl)
		require.ErrorIs(t, err, ErrInviteTTL, ttl)
	}
	invites, err := e.a.ListInvites(ctx)
	require.NoError(t, err)
	assert.Len(t, invites, 3)
}

func TestAuthSignupChecks(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	e.signup(t, "fixture_a")
	tok := e.invite(t)

	_, err := e.a.Signup(ctx, tok, "no spaces", testPassword)
	require.ErrorIs(t, err, ErrInvalidUsername)
	_, err = e.a.Signup(ctx, tok, "FIXTURE_A", testPassword)
	require.ErrorIs(t, err, ErrUsernameTaken)
	_, err = e.a.Signup(ctx, tok, "fixture_b", "short")
	require.ErrorIs(t, err, auth.ErrPasswordTooShort)
	_, err = e.a.Signup(ctx, tok, "fixture_b", "fixture_b")
	require.ErrorIs(t, err, auth.ErrPasswordIsUsername)
	// Without a valid invite, nothing is said about the username.
	_, err = e.a.Signup(ctx, "inv_"+strings.Repeat("A", 43), "fixture_a", testPassword)
	require.ErrorIs(t, err, ErrInvalidToken)

	// A second step 1 replaces the first.
	first, err := e.a.Signup(ctx, tok, "fixture_b", testPassword)
	require.NoError(t, err)
	second, err := e.a.Signup(ctx, tok, "fixture_c", testPassword)
	require.NoError(t, err)
	_, err = e.a.ConfirmSignup(ctx, first.Token, auth.TOTPCode(decodeSecret(t, first.TOTPSecret), e.clk.Now()))
	require.ErrorIs(t, err, ErrInvalidToken)
	secret := decodeSecret(t, second.TOTPSecret)
	_, err = e.a.ConfirmSignup(ctx, second.Token, "000000")
	require.ErrorIs(t, err, ErrInvalidCredentials)

	// The name was taken between the two steps (another invite's confirm).
	other, err := e.a.Signup(ctx, e.invite(t), "fixture_c", testPassword)
	require.NoError(t, err)
	_, err = e.a.ConfirmSignup(ctx, other.Token, auth.TOTPCode(decodeSecret(t, other.TOTPSecret), e.clk.Now()))
	require.NoError(t, err)
	_, err = e.a.ConfirmSignup(ctx, second.Token, auth.TOTPCode(secret, e.clk.Now()))
	require.ErrorIs(t, err, ErrUsernameTaken)

	// An enrollment expires after EnrollmentTTL.
	third, err := e.a.Signup(ctx, tok, "fixture_d", testPassword)
	require.NoError(t, err)
	e.advance(EnrollmentTTL)
	_, err = e.a.ConfirmSignup(ctx, third.Token, auth.TOTPCode(decodeSecret(t, third.TOTPSecret), e.clk.Now()))
	require.ErrorIs(t, err, ErrInvalidToken)
}

func TestAuthEnrollmentEndsWithInvite(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	l, err := e.a.CreateInvite(ctx, 5*time.Minute)
	require.NoError(t, err)
	en, err := e.a.Signup(ctx, inviteToken(t, l), "fixture_a", testPassword)
	require.NoError(t, err)
	assert.Equal(t, l.ExpiresAt.UTC(), en.ExpiresAt.UTC())
}

func TestAuthResetPassword(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	u := e.signup(t, "fixture_user")
	p := e.login(t, u)

	r, err := e.a.CreateReset(ctx, u.id, model.ResetPassword, time.Hour)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(r.URL, "https://mirror.example.com/reset#token=inv_"))
	assert.True(t, strings.HasSuffix(r.URL, "&scope=password"), "the link names its scope")
	tok := inviteToken(t, r)
	_, err = e.a.StartReset(ctx, tok, "")
	require.ErrorIs(t, err, ErrResetPassword)
	_, err = e.a.Signup(ctx, tok, "fixture_x", testPassword)
	require.ErrorIs(t, err, ErrInvalidToken, "a reset invite is not a signup")
	_, err = e.a.StartReset(ctx, tok, "fixture_user")
	require.ErrorIs(t, err, auth.ErrPasswordIsUsername)

	newPassword := testPassword2
	en, err := e.a.StartReset(ctx, tok, newPassword)
	require.NoError(t, err)
	assert.Empty(t, en.TOTPSecret, "the TOTP secret stays")
	e.nextStep()
	c, err := e.a.ConfirmReset(ctx, en.Token, auth.TOTPCode(u.secret, e.clk.Now()))
	require.NoError(t, err)
	assert.Equal(t, u.id, c.UserID)
	assert.Empty(t, c.RecoveryCodes, "a password reset keeps the recovery codes")
	acct, err := e.a.Account(ctx, u.id)
	require.NoError(t, err)
	assert.Equal(t, auth.RecoveryCodeCount, acct.RecoveryCodesLeft)

	requireUserHash(t, e, u.id, r.ID, newPassword)

	_, err = e.a.Authenticate(ctx, p.AccessToken)
	require.ErrorIs(t, err, ErrInvalidToken, "a reset revokes every session")
	_, err = e.a.Login(ctx, u.name, testPassword)
	require.ErrorIs(t, err, ErrInvalidCredentials)
	e.advance(time.Minute)
	_, err = e.a.Login(ctx, u.name, newPassword)
	require.NoError(t, err)
	_, err = e.a.StartReset(ctx, tok, newPassword)
	require.ErrorIs(t, err, ErrInvalidToken, "used")
}

// TestAuthResetCodeBackoff: a password-only reset confirms with the account's own TOTP, so a
// wrong code counts against the account and its backoff refuses the next try, as on a login.
func TestAuthResetCodeBackoff(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	u := e.signup(t, "fixture_user")
	r, err := e.a.CreateReset(ctx, u.id, model.ResetPassword, time.Hour)
	require.NoError(t, err)
	en, err := e.a.StartReset(ctx, inviteToken(t, r), testPassword2)
	require.NoError(t, err)

	_, err = e.a.ConfirmReset(ctx, en.Token, "000000")
	require.ErrorIs(t, err, ErrInvalidCredentials)
	acct, err := e.a.Account(ctx, u.id)
	require.NoError(t, err)
	assert.Equal(t, 1, acct.FailedAttempts, "a wrong code counts against the account")

	_, err = e.a.ConfirmReset(ctx, en.Token, auth.TOTPCode(u.secret, e.clk.Now()))
	var rl RateLimitedError
	require.ErrorAs(t, err, &rl, "the account's backoff applies")
	assert.Positive(t, rl.RetryAfter)

	e.advance(rl.RetryAfter)
	e.nextStep()
	_, err = e.a.ConfirmReset(ctx, en.Token, auth.TOTPCode(u.secret, e.clk.Now()))
	require.NoError(t, err)
	acct, err = e.a.Account(ctx, u.id)
	require.NoError(t, err)
	assert.Zero(t, acct.FailedAttempts, "a reset clears the failures")
}

func TestAuthResetTOTP(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	u := e.signup(t, "fixture_user")
	r, err := e.a.CreateReset(ctx, u.id, model.ResetTOTP, 0)
	require.NoError(t, err)
	tok := inviteToken(t, r)
	_, err = e.a.StartReset(ctx, tok, testPassword)
	require.ErrorIs(t, err, ErrResetPassword)
	en, err := e.a.StartReset(ctx, tok, "")
	require.NoError(t, err)
	require.NotEmpty(t, en.TOTPSecret)
	assert.Contains(t, en.OTPAuthURI, ":fixture_user?")
	secret := decodeSecret(t, en.TOTPSecret)
	e.nextStep()
	_, err = e.a.ConfirmReset(ctx, en.Token, auth.TOTPCode(u.secret, e.clk.Now()))
	require.ErrorIs(t, err, ErrInvalidCredentials, "the old secret doesn't confirm")
	c, err := e.a.ConfirmReset(ctx, en.Token, auth.TOTPCode(secret, e.clk.Now()))
	require.NoError(t, err)
	assert.Len(t, c.RecoveryCodes, auth.RecoveryCodeCount, "a TOTP reset issues new recovery codes")

	// The new secret logs in; the old one no longer does.
	ch, err := e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	e.nextStep()
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{Code: auth.TOTPCode(u.secret, e.clk.Now())})
	require.ErrorIs(t, err, ErrInvalidCredentials)
	e.advance(time.Minute)
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{Code: auth.TOTPCode(secret, e.clk.Now())})
	require.NoError(t, err)

	// Old recovery codes are gone too.
	ch, err = e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{RecoveryCode: u.codes[0]})
	require.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestAuthResetChecks(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	u := e.signup(t, "fixture_user")
	_, err := e.a.CreateReset(ctx, u.id, "everything", 0)
	require.ErrorIs(t, err, ErrResetScope)
	_, err = e.a.CreateReset(ctx, u.id+100, model.ResetBoth, 0)
	require.Error(t, err)

	// Deleting the user deletes its reset invite.
	r, err := e.a.CreateReset(ctx, u.id, model.ResetBoth, 0)
	require.NoError(t, err)
	en, err := e.a.StartReset(ctx, inviteToken(t, r), "fixture new staple battery")
	require.NoError(t, err)
	require.NoError(t, e.a.DeleteUser(ctx, u.id))
	_, err = e.a.ConfirmReset(ctx, en.Token, auth.TOTPCode(decodeSecret(t, en.TOTPSecret), e.clk.Now()))
	require.ErrorIs(t, err, ErrInvalidToken)
}
