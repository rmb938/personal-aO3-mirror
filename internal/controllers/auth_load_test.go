package controllers

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// Load tests for the auth limits (docs/part2-api.md, stage 17b): parallel attempts at one
// instant of the fake clock. The users store runs one write transaction at a time, so each
// check-and-update below is exact, not statistical.

// loadN is how many attempts a burst runs at once.
const loadN = 8

// parallel runs f(0..n-1) at once (each goroutine waits on one start signal) and returns the
// errors in call order.
func parallel(n int, f func(i int) error) []error {
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			errs[i] = f(i)
		})
	}
	close(start)
	wg.Wait()
	return errs
}

// authOutcome names an auth error the way attempt does; anything else fails the test.
func authOutcome(t *testing.T, err error) string {
	t.Helper()
	if rl, ok := errors.AsType[RateLimitedError](err); ok {
		assert.Positive(t, rl.RetryAfter)
		return "rate_limited"
	}
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidCredentials):
		return "invalid"
	case errors.Is(err, ErrInvalidToken):
		return "invalid_token"
	}
	t.Errorf("unexpected error: %v", err)
	return err.Error()
}

// tally counts the outcomes of a burst.
func tally(t *testing.T, errs []error) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, err := range errs {
		out[authOutcome(t, err)]++
	}
	return out
}

func failedAttempts(t *testing.T, e *authEnv, id int64) int {
	t.Helper()
	acct, err := e.a.Account(context.Background(), id)
	require.NoError(t, err)
	return acct.FailedAttempts
}

// TestAuthLoginBackoffParallel: of a burst of wrong passwords exactly one is checked and
// counted, the rest wait; an unknown name answers the same burst identically.
func TestAuthLoginBackoffParallel(t *testing.T) {
	e := newAuthEnv(t)
	u := e.signup(t, "fixture_user")
	ctx := context.Background()
	run := func(name string) []map[string]int {
		var out []map[string]int
		for range 4 {
			out = append(out, tally(t, parallel(loadN, func(int) error {
				_, err := e.a.Login(ctx, name, "fixture wrong password")
				return err
			})))
			e.advance(backoffMax)
		}
		return out
	}
	known := run(u.name)
	want := map[string]int{"invalid": 1, "rate_limited": loadN - 1}
	assert.Equal(t, []map[string]int{want, want, want, want}, known)
	assert.Equal(t, 4, failedAttempts(t, e, u.id), "one failure per burst, none lost")
	assert.Equal(t, known, run("fixture_nobody"), "an unknown name looks the same")
}

// TestAuthDisabledAfterMaxFailuresParallel bursts wrong passwords until the account is
// disabled: every burst counts exactly one failure, so it takes MaxFailedAttempts bursts, and
// no burst of the right password gets through afterwards.
func TestAuthDisabledAfterMaxFailuresParallel(t *testing.T) {
	e := newAuthEnv(t)
	u := e.signup(t, "fixture_user")
	ctx := context.Background()
	for i := range MaxFailedAttempts {
		got := tally(t, parallel(loadN, func(int) error {
			_, err := e.a.Login(ctx, u.name, "fixture wrong password")
			return err
		}))
		require.Equal(t, map[string]int{"invalid": 1, "rate_limited": loadN - 1}, got, "burst %d", i+1)
		require.Equal(t, i+1, failedAttempts(t, e, u.id), "burst %d", i+1)
		acct, err := e.a.Account(ctx, u.id)
		require.NoError(t, err)
		require.Equal(t, i+1 == MaxFailedAttempts, !acct.DisabledAt.IsZero(), "burst %d", i+1)
		e.advance(backoffMax)
	}
	assert.Equal(t, 1, strings.Count(e.logs.String(), `"event":"account_disabled"`))

	got := tally(t, parallel(loadN, func(int) error {
		_, err := e.a.Login(ctx, u.name, testPassword)
		return err
	}))
	assert.Equal(t, map[string]int{"invalid": 1, "rate_limited": loadN - 1}, got, "disabled: the right password fails too")
}

// TestAuthSecondFactorBackoffParallel: a burst of wrong codes on one challenge counts one
// failure (and one challenge attempt); the rest wait without using up the challenge.
func TestAuthSecondFactorBackoffParallel(t *testing.T) {
	e := newAuthEnv(t)
	u := e.signup(t, "fixture_user")
	ctx := context.Background()
	e.nextStep()
	ch, err := e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	for i := range MaxChallengeAttempts - 1 {
		got := tally(t, parallel(loadN, func(int) error {
			_, err := e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{Code: "000000"})
			return err
		}))
		require.Equal(t, map[string]int{"invalid": 1, "rate_limited": loadN - 1}, got, "burst %d", i+1)
		require.Equal(t, i+1, failedAttempts(t, e, u.id))
		e.advance(backoffDelay(i + 1)) // well inside ChallengeTTL
	}
	e.nextStep()
	_, err = e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{Code: auth.TOTPCode(u.secret, e.clk.Now())})
	require.NoError(t, err, "the challenge's last attempt is left")
}

// TestAuthChallengeSingleUseParallel: of a burst of right answers to one challenge exactly one
// starts a session.
func TestAuthChallengeSingleUseParallel(t *testing.T) {
	e := newAuthEnv(t)
	u := e.signup(t, "fixture_user")
	ctx := context.Background()
	sessions := func() int {
		s, err := e.a.UserSessions(ctx, u.id)
		require.NoError(t, err)
		return len(s)
	}

	// Distinct recovery codes, so only the challenge is shared.
	e.nextStep()
	ch, err := e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	got := tally(t, parallel(len(u.codes), func(i int) error {
		_, err := e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{RecoveryCode: u.codes[i]})
		return err
	}))
	assert.Equal(t, map[string]int{"ok": 1, "invalid_token": len(u.codes) - 1}, got, "recovery codes")
	assert.Equal(t, 1, sessions())

	// The same TOTP code.
	e.nextStep()
	ch, err = e.a.Login(ctx, u.name, testPassword)
	require.NoError(t, err)
	code := auth.TOTPCode(u.secret, e.clk.Now())
	got = tally(t, parallel(loadN, func(int) error {
		_, err := e.a.LoginSecondFactor(ctx, ch.Token, SecondFactor{Code: code})
		return err
	}))
	assert.Equal(t, map[string]int{"ok": 1, "invalid_token": loadN - 1}, got, "totp")
	assert.Equal(t, 2, sessions())
}

// TestAuthEnrollmentSingleUseParallel: parallel signups or resets on one invite leave one
// enrollment, and of parallel confirmations exactly one completes.
func TestAuthEnrollmentSingleUseParallel(t *testing.T) {
	e := newAuthEnv(t)
	ctx := context.Background()
	confirmAll := func(confirm func(ctx context.Context, token, code string) (Confirmed, error), ens []Enrollment, secret func(Enrollment) []byte) map[string]int {
		return tally(t, parallel(len(ens), func(i int) error {
			_, err := confirm(ctx, ens[i].Token, auth.TOTPCode(secret(ens[i]), e.clk.Now()))
			return err
		}))
	}
	newSecret := func(en Enrollment) []byte { return decodeSecret(t, en.TOTPSecret) }
	users := func() int {
		us, err := e.a.ListUsers(ctx)
		require.NoError(t, err)
		return len(us)
	}

	// Signup: each start replaces the last, so one token is left to confirm.
	invite := e.invite(t)
	ens := make([]Enrollment, loadN)
	errs := parallel(loadN, func(i int) error {
		var err error
		ens[i], err = e.a.Signup(ctx, invite, "fixture_user"+strconv.Itoa(i), testPassword)
		return err
	})
	assert.Equal(t, map[string]int{"ok": loadN}, tally(t, errs), "signup starts")
	assert.Equal(t, map[string]int{"ok": 1, "invalid_token": loadN - 1}, confirmAll(e.a.ConfirmSignup, ens, newSecret), "signup confirms")
	assert.Equal(t, 1, users())

	// One enrollment token confirmed many times at once.
	en, err := e.a.Signup(ctx, e.invite(t), "fixture_other", testPassword)
	require.NoError(t, err)
	same := make([]Enrollment, loadN)
	for i := range same {
		same[i] = en
	}
	assert.Equal(t, map[string]int{"ok": 1, "invalid_token": loadN - 1}, confirmAll(e.a.ConfirmSignup, same, newSecret), "one token")
	assert.Equal(t, 2, users())

	// Reset: the same for a reset invite.
	us, err := e.a.ListUsers(ctx)
	require.NoError(t, err)
	link, err := e.a.CreateReset(ctx, us[0].ID, model.ResetTOTP, 0)
	require.NoError(t, err)
	reset := inviteToken(t, link)
	errs = parallel(loadN, func(i int) error {
		var err error
		ens[i], err = e.a.StartReset(ctx, reset, "")
		return err
	})
	assert.Equal(t, map[string]int{"ok": loadN}, tally(t, errs), "reset starts")
	e.nextStep() // the signup used this step's code
	assert.Equal(t, map[string]int{"ok": 1, "invalid_token": loadN - 1}, confirmAll(e.a.ConfirmReset, ens, newSecret), "reset confirms")
}

// TestAuthRefreshParallel: of parallel refreshes with one token exactly one gets a pair; the
// rest present the token it replaced, which is reuse, so the session is revoked.
func TestAuthRefreshParallel(t *testing.T) {
	for _, n := range []int{2, loadN} {
		e := newAuthEnv(t)
		u := e.signup(t, "fixture_user")
		ctx := context.Background()
		p := e.login(t, u)
		pairs := make([]TokenPair, n)
		got := tally(t, parallel(n, func(i int) error {
			var err error
			pairs[i], err = e.a.Refresh(ctx, p.RefreshToken)
			return err
		}))
		require.Equal(t, map[string]int{"ok": 1, "invalid_token": n - 1}, got, "n=%d", n)
		assert.Equal(t, n-1, strings.Count(e.logs.String(), `"reason":"refresh_reuse"`), "n=%d", n)
		for _, pair := range pairs {
			if pair.RefreshToken != "" {
				_, err := e.a.Refresh(ctx, pair.RefreshToken)
				require.ErrorIs(t, err, ErrInvalidToken, "n=%d: the winner's session is revoked", n)
			}
		}
	}
}
