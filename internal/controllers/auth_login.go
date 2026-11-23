package controllers

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// LoginChallenge is a login's first step passed: present Token with a second factor before
// ExpiresAt.
type LoginChallenge struct {
	Token     string
	ExpiresAt time.Time
}

// TokenPair is a logged-in session's credentials.
type TokenPair struct {
	UserID           int64
	SessionID        int64
	AccessToken      string
	AccessExpiresAt  time.Time
	AccessTTL        time.Duration // expires_in
	RefreshToken     string
	RefreshExpiresAt time.Time
}

// Login is a login's first step: username and password. An unknown user, a wrong password and
// a disabled account are all ErrInvalidCredentials, with the same work (one Argon2id verify)
// and the same backoff; a backoff refusal is RateLimitedError, before any hashing.
func (a *Auth) Login(ctx context.Context, username, password string) (LoginChallenge, error) {
	now := a.now()
	name, valid := NormalizeUsername(username)
	var u model.User
	known := false
	if valid {
		var err error
		u, err = store.ReadUsersValue(ctx, a.users, func(ctx context.Context, tx *store.UsersReadTx) (model.User, error) {
			return tx.UserByName(ctx, name)
		})
		switch {
		case err == nil:
			known = true
		case !errors.Is(err, store.ErrNoUser):
			return LoginChallenge{}, err
		}
	}

	if !known {
		mac := a.names.key(name)
		if wait, ok := waitFor(a.names.next(mac), now); ok {
			a.event(ctx, slog.LevelInfo, "login_refused", "reason", reasonBackoff)
			return LoginChallenge{}, RateLimitedError{RetryAfter: wait}
		}
		if err := a.verifyDummy(ctx, password); err != nil {
			return LoginChallenge{}, err
		}
		if wait, ok := a.names.fail(mac, now); ok { // a concurrent attempt failed
			return LoginChallenge{}, RateLimitedError{RetryAfter: wait}
		}
		a.event(ctx, slog.LevelInfo, "login_failed", "reason", reasonUnknownUser)
		return LoginChallenge{}, ErrInvalidCredentials
	}

	if wait, ok := waitFor(u.NextAttemptAt, now); ok {
		a.event(ctx, slog.LevelInfo, "login_refused", "reason", reasonBackoff, "user_id", u.ID)
		return LoginChallenge{}, RateLimitedError{RetryAfter: wait}
	}
	good, err := a.verifyPassword(ctx, auth.UserPassword(u.ID), u.PasswordPHC, password)
	if err != nil {
		return LoginChallenge{}, err
	}
	rehash := ""
	if good && !u.Disabled() && a.opts.Hasher.NeedsRehash(u.PasswordPHC) {
		if rehash, err = a.hashPassword(ctx, auth.UserPassword(u.ID), password); err != nil {
			// The login goes on with the old hash; the next one tries again.
			a.event(ctx, slog.LevelWarn, "password_rehash_skipped", "user_id", u.ID, "err", err.Error())
			rehash = ""
		}
	}

	token, sum := auth.NewToken(auth.ChallengeToken)
	expires := now.Add(ChallengeTTL)
	var fail error
	err = a.users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error {
		cur, err := tx.UserByID(ctx, u.ID)
		if errors.Is(err, store.ErrNoUser) { // deleted meanwhile
			fail = ErrInvalidCredentials
			return nil
		} else if err != nil {
			return err
		}
		if cur.PasswordPHC != u.PasswordPHC {
			a.event(ctx, slog.LevelInfo, "login_failed", "reason", reasonChanged, "user_id", u.ID)
			fail = ErrInvalidCredentials
			return nil
		}
		if wait, ok := waitFor(cur.NextAttemptAt, now); ok { // a concurrent attempt failed
			fail = RateLimitedError{RetryAfter: wait}
			return nil
		}
		if !good || cur.Disabled() {
			reason := reasonBadPassword
			if cur.Disabled() {
				reason = reasonDisabled
			}
			a.event(ctx, slog.LevelInfo, "login_failed", "reason", reason, "user_id", u.ID)
			fail = ErrInvalidCredentials
			return a.recordFailure(ctx, tx, cur, now)
		}
		if rehash != "" {
			if _, err := tx.RehashUserPassword(ctx, u.ID, u.PasswordPHC, rehash); err != nil {
				return err
			}
		}
		if err := tx.DeleteExpiredChallenges(ctx, now); err != nil {
			return err
		}
		return tx.CreateChallenge(ctx, sum, model.LoginChallenge{UserID: u.ID, ExpiresAt: expires})
	})
	if err != nil {
		return LoginChallenge{}, err
	}
	if fail != nil {
		return LoginChallenge{}, fail
	}
	a.event(ctx, slog.LevelInfo, "login_challenge_issued", "user_id", u.ID, "rehashed", rehash != "")
	return LoginChallenge{Token: token, ExpiresAt: expires}, nil
}

// LoginSecondFactor is a login's second step: the challenge token and a TOTP code (never one
// used before) or an unused recovery code. It starts a session. A wrong factor is
// ErrInvalidCredentials and counts against the account (backoff); a challenge allows
// MaxChallengeAttempts and then, like an unknown or expired one, is ErrInvalidToken.
func (a *Auth) LoginSecondFactor(ctx context.Context, challengeToken string, f SecondFactor) (TokenPair, error) {
	sum, ok := auth.TokenSum(auth.ChallengeToken, challengeToken)
	if !ok {
		return TokenPair{}, ErrInvalidToken
	}
	now := a.now()
	var pair TokenPair
	var fail error
	err := a.users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error {
		c, err := tx.ChallengeByToken(ctx, sum)
		if errors.Is(err, store.ErrNoChallenge) {
			a.event(ctx, slog.LevelInfo, "login_failed", "reason", reasonUnknownToken, "step", "second_factor")
			fail = ErrInvalidToken
			return nil
		} else if err != nil {
			return err
		}
		if !now.Before(c.ExpiresAt) || c.Attempts >= MaxChallengeAttempts {
			a.event(ctx, slog.LevelInfo, "login_failed", "reason", reasonExpired, "user_id", c.UserID)
			fail = ErrInvalidToken
			return tx.DeleteChallenge(ctx, sum)
		}
		u, err := tx.UserByID(ctx, c.UserID)
		if err != nil {
			return err // the challenge is deleted with its user, so this is not ErrNoUser
		}
		if u.Disabled() { // disabled since the first step
			a.event(ctx, slog.LevelInfo, "login_failed", "reason", reasonDisabled, "user_id", u.ID)
			fail = ErrInvalidCredentials
			return tx.DeleteChallenge(ctx, sum)
		}
		if wait, ok := waitFor(u.NextAttemptAt, now); ok {
			a.event(ctx, slog.LevelInfo, "login_refused", "reason", reasonBackoff, "user_id", u.ID)
			fail = RateLimitedError{RetryAfter: wait}
			return nil
		}
		step, good, err := a.checkSecondFactor(ctx, tx, u, f, now)
		if err != nil {
			return err
		}
		if !good {
			a.event(ctx, slog.LevelInfo, "login_failed", "reason", reasonBadCode, "user_id", u.ID,
				"recovery_code", f.RecoveryCode != "", "attempt", c.Attempts+1)
			fail = ErrInvalidCredentials
			if c.Attempts+1 >= MaxChallengeAttempts {
				err = tx.DeleteChallenge(ctx, sum)
			} else {
				err = tx.SetChallengeAttempts(ctx, sum, c.Attempts+1)
			}
			return errors.Join(err, a.recordFailure(ctx, tx, u, now))
		}
		if step > 0 {
			if err := tx.SetUserTOTPStep(ctx, u.ID, step); err != nil {
				return err
			}
		}
		if err := tx.RecordUserLogin(ctx, u.ID, now); err != nil {
			return err
		}
		if err := tx.DeleteChallenge(ctx, sum); err != nil {
			return err
		}
		pair, err = a.startSession(ctx, tx, u.ID, now)
		return err
	})
	if err != nil {
		return TokenPair{}, err
	}
	if fail != nil {
		return TokenPair{}, fail
	}
	a.event(ctx, slog.LevelInfo, "login_succeeded", "user_id", pair.UserID, "session_id", pair.SessionID,
		"recovery_code", f.RecoveryCode != "")
	return pair, nil
}

// startSession creates a session with a new refresh token and signs its first access token.
func (a *Auth) startSession(ctx context.Context, tx *store.UsersTx, userID int64, now time.Time) (TokenPair, error) {
	refresh, sum := auth.NewToken(auth.RefreshToken)
	expires := a.sessionExpiry(now, now)
	if err := tx.DeleteExpiredSessions(ctx, now); err != nil {
		return TokenPair{}, err
	}
	sid, err := tx.CreateSession(ctx, userID, sum, now, expires)
	if err != nil {
		return TokenPair{}, err
	}
	return a.pair(userID, sid, refresh, expires)
}

// sessionExpiry is when a session created at created and refreshed at now ends: the refresh
// TTL from now (the idle limit), but never past MaxSessionAge from its login.
func (a *Auth) sessionExpiry(created, now time.Time) time.Time {
	idle, hard := now.Add(a.opts.RefreshTTL), created.Add(a.opts.MaxSessionAge)
	if hard.Before(idle) {
		return hard
	}
	return idle
}

func (a *Auth) pair(userID, sessionID int64, refresh string, refreshExpires time.Time) (TokenPair, error) {
	access, claims, err := a.keys().issuer.Issue(userID, sessionID)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{UserID: userID, SessionID: sessionID, AccessToken: access, AccessExpiresAt: claims.ExpiresAt,
		AccessTTL: a.opts.AccessTTL, RefreshToken: refresh, RefreshExpiresAt: refreshExpires}, nil
}

// Refresh trades a session's refresh token for a new pair (rotation: a new refresh token each
// time, and the old one stops working). The session is extended by the refresh TTL, up to
// MaxSessionAge after its login; then the user logs in again. Presenting the previous token
// again means it was copied: the session is revoked (reuse detection). Every refusal is
// ErrInvalidToken.
func (a *Auth) Refresh(ctx context.Context, refreshToken string) (TokenPair, error) {
	sum, ok := auth.TokenSum(auth.RefreshToken, refreshToken)
	if !ok {
		return TokenPair{}, ErrInvalidToken
	}
	now := a.now()
	var pair TokenPair
	var reason string
	var sessionID int64
	err := a.users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error {
		s, err := tx.SessionByRefresh(ctx, sum)
		if errors.Is(err, store.ErrNoSession) {
			reason = reasonUnknownToken
			prev, err := tx.SessionByPrevRefresh(ctx, sum)
			if errors.Is(err, store.ErrNoSession) {
				return nil
			} else if err != nil {
				return err
			}
			reason, sessionID = reasonRefreshReuse, prev.ID
			return tx.RevokeSession(ctx, prev.ID, now)
		} else if err != nil {
			return err
		}
		sessionID = s.ID
		if !s.Active(now) {
			reason = reasonSessionRevoked
			return nil
		}
		u, err := tx.UserByID(ctx, s.UserID)
		if err != nil {
			return err
		}
		if u.Disabled() {
			reason = reasonDisabled
			return nil
		}
		next, nextSum := auth.NewToken(auth.RefreshToken)
		expires := a.sessionExpiry(s.CreatedAt, now)
		if ok, err := tx.RotateSession(ctx, s.ID, sum, nextSum, now, expires); err != nil || !ok {
			return errors.Join(err, errors.New("controllers: session changed during refresh"))
		}
		pair, err = a.pair(u.ID, s.ID, next, expires)
		return err
	})
	if err != nil {
		return TokenPair{}, err
	}
	if reason != "" {
		level := slog.LevelInfo
		if reason == reasonRefreshReuse {
			level = slog.LevelWarn
		}
		a.event(ctx, level, "refresh_refused", "reason", reason, "session_id", sessionID)
		return TokenPair{}, ErrInvalidToken
	}
	a.event(ctx, slog.LevelDebug, "session_refreshed", "user_id", pair.UserID, "session_id", pair.SessionID)
	return pair, nil
}

// Logout revokes the caller's own session.
func (a *Auth) Logout(ctx context.Context, p Principal) error {
	err := a.users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error {
		return tx.RevokeUserSession(ctx, p.UserID, p.SessionID, a.now())
	})
	if err == nil {
		a.event(ctx, slog.LevelInfo, "logout", "user_id", p.UserID, "session_id", p.SessionID)
	}
	return err
}
