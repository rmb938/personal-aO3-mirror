package controllers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

func TestAuthChangePassword(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	u := e.signup(t, "fixture_user")
	p1 := e.login(t, u)
	p2 := e.login(t, u)
	me := Principal{UserID: u.id, SessionID: p1.SessionID}
	newPassword := testPassword2

	e.nextStep()
	err := e.a.ChangePassword(ctx, me, "fixture wrong password", newPassword, auth.TOTPCode(u.secret, e.clk.Now()))
	require.ErrorIs(t, err, ErrInvalidCredentials)
	err = e.a.ChangePassword(ctx, me, testPassword, newPassword, auth.TOTPCode(u.secret, e.clk.Now()))
	_, limited := errors.AsType[RateLimitedError](err)
	require.True(t, limited, "a failure backs off like a login: %v", err)
	e.advance(time.Second)
	err = e.a.ChangePassword(ctx, me, testPassword, newPassword, "000000")
	require.ErrorIs(t, err, ErrInvalidCredentials)
	err = e.a.ChangePassword(ctx, me, testPassword, "short", auth.TOTPCode(u.secret, e.clk.Now()))
	require.ErrorIs(t, err, auth.ErrPasswordTooShort)

	e.advance(time.Minute)
	require.NoError(t, e.a.ChangePassword(ctx, me, testPassword, newPassword, auth.TOTPCode(u.secret, e.clk.Now())))
	_, err = e.a.Authenticate(ctx, p1.AccessToken)
	require.NoError(t, err, "the caller's session stays")
	_, err = e.a.Authenticate(ctx, p2.AccessToken)
	require.ErrorIs(t, err, ErrInvalidToken, "every other session is revoked")
	acct, err := e.a.Account(ctx, u.id)
	require.NoError(t, err)
	assert.Equal(t, e.clk.Now(), acct.PasswordChangedAt.UTC())
	assert.Zero(t, acct.FailedAttempts)

	_, err = e.a.Login(ctx, u.name, newPassword)
	require.NoError(t, err)
}

func TestAuthRegenerateRecoveryCodes(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	u := e.signup(t, "fixture_user")
	p := e.login(t, u)
	me := Principal{UserID: u.id, SessionID: p.SessionID}
	_, err := e.a.RegenerateRecoveryCodes(ctx, me, auth.TOTPCode(u.secret, e.clk.Now()))
	require.ErrorIs(t, err, ErrInvalidCredentials, "the login used this step's code")
	e.advance(time.Minute)
	codes, err := e.a.RegenerateRecoveryCodes(ctx, me, auth.TOTPCode(u.secret, e.clk.Now()))
	require.NoError(t, err)
	require.Len(t, codes, auth.RecoveryCodeCount)
	assert.NotEqual(t, u.codes, codes)

	ch, err := e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{RecoveryCode: u.codes[1]})
	require.ErrorIs(t, err, ErrInvalidCredentials, "old codes are gone")
	e.advance(2 * time.Second) // the second failure in a row (a TOTP-only check doesn't reset the count)
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{RecoveryCode: codes[1]})
	require.NoError(t, err)
}

func TestAuthSessionsAreOwned(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	a := e.signup(t, "fixture_a")
	b := e.signup(t, "fixture_b")
	pa := e.login(t, a)
	pb := e.login(t, b)

	// User A can't revoke user B's session: 404, the same as a missing one.
	require.ErrorIs(t, e.a.RevokeUserSession(ctx, a.id, pb.SessionID), store.ErrNoSession)
	require.ErrorIs(t, e.a.RevokeUserSession(ctx, a.id, 9999), store.ErrNoSession)
	_, err := e.a.Authenticate(ctx, pb.AccessToken)
	require.NoError(t, err)

	sessions, err := e.a.UserSessions(ctx, a.id)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, pa.SessionID, sessions[0].ID)
	require.NoError(t, e.a.RevokeUserSession(ctx, a.id, pa.SessionID))
	sessions, err = e.a.UserSessions(ctx, a.id)
	require.NoError(t, err)
	assert.Empty(t, sessions)
	_, err = e.a.UserSessions(ctx, 9999)
	require.ErrorIs(t, err, store.ErrNoUser)

	// The admin can revoke any session.
	require.NoError(t, e.a.RevokeSession(ctx, pb.SessionID))
	_, err = e.a.Authenticate(ctx, pb.AccessToken)
	require.ErrorIs(t, err, ErrInvalidToken)
	require.ErrorIs(t, e.a.RevokeSession(ctx, 9999), store.ErrNoSession)
}

func TestAuthAdminUsers(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	a := e.signup(t, "fixture_a")
	b := e.signup(t, "fixture_b")
	pa := e.login(t, a)
	pb := e.login(t, b)

	users, err := e.a.ListUsers(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.Equal(t, "fixture_a", users[0].Username)
	assert.Equal(t, auth.RecoveryCodeCount, users[0].RecoveryCodesLeft)

	require.NoError(t, e.a.DisableUser(ctx, a.id))
	_, err = e.a.Authenticate(ctx, pa.AccessToken)
	require.ErrorIs(t, err, ErrInvalidToken)
	_, err = e.a.Refresh(ctx, pa.RefreshToken)
	require.ErrorIs(t, err, ErrInvalidToken)
	_, err = e.a.Login(ctx, a.name, testPassword)
	require.ErrorIs(t, err, ErrInvalidCredentials)
	require.NoError(t, e.a.EnableUser(ctx, a.id))
	e.advance(time.Second)
	_, err = e.a.Login(ctx, a.name, testPassword)
	require.NoError(t, err)
	_, err = e.a.Refresh(ctx, pa.RefreshToken)
	require.ErrorIs(t, err, ErrInvalidToken, "disabling revoked the sessions for good")

	require.NoError(t, e.a.DeleteUser(ctx, b.id))
	_, err = e.a.Authenticate(ctx, pb.AccessToken)
	require.ErrorIs(t, err, ErrInvalidToken)
	require.ErrorIs(t, e.a.DeleteUser(ctx, b.id), store.ErrNoUser)
	require.ErrorIs(t, e.a.DisableUser(ctx, b.id), store.ErrNoUser)
	require.ErrorIs(t, e.a.EnableUser(ctx, b.id), store.ErrNoUser)
	require.ErrorIs(t, e.a.RevokeInvite(ctx, 9999), store.ErrNoInvite)
}
