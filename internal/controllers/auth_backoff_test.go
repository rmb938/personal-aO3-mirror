package controllers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
)

func TestBackoffDelay(t *testing.T) {
	for n, want := range map[int]time.Duration{0: 0, 1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second,
		10: 512 * time.Second, 11: 15 * time.Minute, 100: 15 * time.Minute} {
		assert.Equal(t, want, backoffDelay(n), n)
	}
	now := time.Date(2026, 11, 15, 12, 0, 0, 300_000_000, time.UTC)
	assert.Equal(t, time.Date(2026, 11, 15, 12, 0, 2, 0, time.UTC), nextAttempt(now, 1), "rounded up")
	d, ok := waitFor(nextAttempt(now, 1), now)
	assert.True(t, ok)
	assert.Equal(t, 2*time.Second, d, "Retry-After rounds up")
	_, ok = waitFor(now, now)
	assert.False(t, ok)
}

func TestNormalizeUsername(t *testing.T) {
	for in, want := range map[string]string{"abc": "abc", "Fixture_User-1": "fixture_user-1", "ABC": "abc"} {
		got, ok := NormalizeUsername(in)
		assert.True(t, ok, in)
		assert.Equal(t, want, got)
	}
	// The Kelvin sign lower-cases to "k" with Unicode rules; here it is refused, never folded.
	for _, in := range []string{"", "ab", "a b c", "abc!", "Kelvin", "ünï", string(make([]byte, 33))} {
		_, ok := NormalizeUsername(in)
		assert.False(t, ok, "%q", in)
	}
}

func TestNameBackoffEvicts(t *testing.T) {
	b := newNameBackoff(2)
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	k := func(i byte) [32]byte { return [32]byte{i} }
	b.fail(k(1), now)
	b.fail(k(1), now)
	b.fail(k(2), now)
	assert.Equal(t, now.Add(2*time.Second), b.next(k(1))) // k(1) is now the most recent
	b.fail(k(3), now)
	assert.True(t, b.next(k(2)).IsZero(), "least recently used evicted")
	assert.False(t, b.next(k(1)).IsZero())
}

// attempt returns what a login attempt looks like from outside: the error kind and the wait.
func attempt(t *testing.T, e *authEnv, name, password string) (string, time.Duration) {
	t.Helper()
	_, err := e.a.Login(context.Background(), name, password)
	if rl, ok := errors.AsType[RateLimitedError](err); ok {
		return "rate_limited", rl.RetryAfter
	}
	switch {
	case err == nil:
		return "ok", 0
	case errors.Is(err, ErrInvalidCredentials):
		return "invalid", 0
	}
	t.Fatalf("unexpected error: %v", err)
	return "", 0
}

// TestAuthUnknownUserIndistinguishable runs the same attempts against an existing account (wrong
// password), an unknown name and a malformed name: the answers and waits are identical.
func TestAuthUnknownUserIndistinguishable(t *testing.T) {
	e := newAuthEnv(t)
	e.signup(t, "fixture_user")
	type step struct {
		what string
		wait time.Duration
	}
	run := func(name string) []step {
		var out []step
		for i := range 6 {
			what, wait := attempt(t, e, name, "fixture wrong password")
			out = append(out, step{what, wait})
			what, wait = attempt(t, e, name, "fixture wrong password") // at once: refused
			out = append(out, step{what, wait})
			e.advance(backoffDelay(i + 1))
		}
		return out
	}
	known := run("fixture_user")
	assert.Equal(t, []step{{"invalid", 0}, {"rate_limited", time.Second}, {"invalid", 0}, {"rate_limited", 2 * time.Second},
		{"invalid", 0}, {"rate_limited", 4 * time.Second}}, known[:6])
	assert.Equal(t, known, run("fixture_nobody"))
	assert.Equal(t, known, run("x!"))
}

func TestAuthBackoffResetsOnLogin(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	u := e.signup(t, "fixture_user")
	what, _ := attempt(t, e, u.name, "fixture wrong password")
	require.Equal(t, "invalid", what)
	what, wait := attempt(t, e, u.name, testPassword)
	require.Equal(t, "rate_limited", what, "even the right password waits")
	e.advance(wait)

	// A right password alone doesn't reset the count (else TOTP could be guessed without limit).
	ch, err := e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	acct, err := e.a.Account(ctx, u.id)
	require.NoError(t, err)
	assert.Equal(t, 1, acct.FailedAttempts)
	e.nextStep()
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{Code: auth.TOTPCode(u.secret, e.clk.Now())})
	require.NoError(t, err)
	acct, err = e.a.Account(ctx, u.id)
	require.NoError(t, err)
	assert.Zero(t, acct.FailedAttempts)
	what, _ = attempt(t, e, u.name, testPassword)
	assert.Equal(t, "ok", what)
}

func TestAuthSecondFactorFailuresBackOff(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	u := e.signup(t, "fixture_user")
	e.nextStep() // the signup used this step's code
	ch, err := e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{Code: "000000"})
	require.ErrorIs(t, err, ErrInvalidCredentials)
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{Code: auth.TOTPCode(u.secret, e.clk.Now())})
	rl, ok := errors.AsType[RateLimitedError](err)
	require.True(t, ok, "%v", err)
	e.advance(rl.RetryAfter)
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{Code: auth.TOTPCode(u.secret, e.clk.Now())})
	require.NoError(t, err)
}

func TestAuthDisabledAfterMaxFailures(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	u := e.signup(t, "fixture_user")
	p := e.login(t, u)
	require.NoError(t, e.users.AutoCommitForTestOnly().SetUserAttempts(ctx, u.id, MaxFailedAttempts-1, time.Time{}))

	what, _ := attempt(t, e, u.name, "fixture wrong password")
	require.Equal(t, "invalid", what)
	acct, err := e.a.Account(ctx, u.id)
	require.NoError(t, err)
	assert.False(t, acct.DisabledAt.IsZero())
	assert.Contains(t, e.logs.String(), `"event":"account_disabled"`)
	_, err = e.a.Authenticate(ctx, p.AccessToken)
	require.ErrorIs(t, err, ErrInvalidToken, "a disabled account's tokens stop")

	// The right password now fails exactly like a wrong one.
	e.advance(backoffMax)
	what, _ = attempt(t, e, u.name, testPassword)
	assert.Equal(t, "invalid", what)

	require.NoError(t, e.a.EnableUser(ctx, u.id))
	what, _ = attempt(t, e, u.name, testPassword)
	assert.Equal(t, "ok", what)
}
