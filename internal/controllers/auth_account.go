package controllers

import (
	"context"
	"log/slog"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// UserInfo is an account without its secrets.
type UserInfo struct {
	ID                int64
	Username          string
	CreatedAt         time.Time
	LastLoginAt       time.Time
	PasswordChangedAt time.Time
	DisabledAt        time.Time
	FailedAttempts    int
	RecoveryCodesLeft int
}

func userInfo(u model.User, codesLeft int) UserInfo {
	return UserInfo{ID: u.ID, Username: u.Username, CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt,
		PasswordChangedAt: u.PasswordChangedAt, DisabledAt: u.DisabledAt, FailedAttempts: u.FailedAttempts,
		RecoveryCodesLeft: codesLeft}
}

// Account returns a user's account (store.ErrNoUser when there is none).
func (a *Auth) Account(ctx context.Context, userID int64) (UserInfo, error) {
	return store.ReadUsersValue(ctx, a.users, func(ctx context.Context, tx *store.UsersReadTx) (UserInfo, error) {
		u, err := tx.UserByID(ctx, userID)
		if err != nil {
			return UserInfo{}, err
		}
		n, err := tx.RecoveryCodesLeft(ctx, userID)
		return userInfo(u, n), err
	})
}

// reauth checks a TOTP code for a signed-in user's sensitive change, inside the change's
// transaction: the backoff first, then the code (a failure counts against the account and the
// caller commits it). It returns the accepted step, or fail (ErrInvalidCredentials or a
// RateLimitedError: commit, then return it), or err (roll back).
func (a *Auth) reauth(ctx context.Context, tx *store.UsersTx, u model.User, code string, now time.Time) (step int64, fail, err error) {
	if wait, ok := waitFor(u.NextAttemptAt, now); ok {
		return 0, RateLimitedError{RetryAfter: wait}, nil
	}
	step, ok, err := a.checkSecondFactor(ctx, tx, u, SecondFactor{Code: code}, now)
	if err != nil {
		return 0, nil, err
	}
	if !ok {
		a.event(ctx, slog.LevelInfo, "reauth_failed", "reason", reasonBadCode, "user_id", u.ID)
		return 0, ErrInvalidCredentials, a.recordFailure(ctx, tx, u, now)
	}
	return step, nil, nil
}

// ChangePassword replaces the caller's password, given the current one and a TOTP code, and
// revokes every other session. Wrong credentials are ErrInvalidCredentials and count against
// the account like a failed login.
func (a *Auth) ChangePassword(ctx context.Context, p Principal, current, next, code string) error {
	now := a.now()
	u, err := store.ReadUsersValue(ctx, a.users, func(ctx context.Context, tx *store.UsersReadTx) (model.User, error) {
		return tx.UserByID(ctx, p.UserID)
	})
	if err != nil {
		return err
	}
	if err := auth.CheckPassword(next, u.Username); err != nil {
		return err // first: it says nothing about the credentials and costs no attempt
	}
	if wait, ok := waitFor(u.NextAttemptAt, now); ok {
		return RateLimitedError{RetryAfter: wait}
	}
	good, err := a.verifyPassword(ctx, u.PasswordPHC, current)
	if err != nil {
		return err
	}
	phc := ""
	if good {
		if phc, err = a.hashPassword(ctx, next); err != nil {
			return err
		}
	}
	var fail error
	err = a.users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error {
		cur, err := tx.UserByID(ctx, p.UserID)
		if err != nil {
			return err
		}
		if cur.PasswordPHC != u.PasswordPHC {
			fail = ErrInvalidCredentials
			return nil
		}
		if !good {
			if wait, ok := waitFor(cur.NextAttemptAt, now); ok {
				fail = RateLimitedError{RetryAfter: wait}
				return nil
			}
			a.event(ctx, slog.LevelInfo, "reauth_failed", "reason", reasonBadPassword, "user_id", u.ID)
			fail = ErrInvalidCredentials
			return a.recordFailure(ctx, tx, cur, now)
		}
		var step int64
		if step, fail, err = a.reauth(ctx, tx, cur, code, now); err != nil || fail != nil {
			return err
		}
		for _, f := range []func() error{
			func() error { return tx.SetUserTOTPStep(ctx, u.ID, step) },
			func() error { return tx.SetUserPassword(ctx, u.ID, phc, now) },
			func() error { return tx.SetUserAttempts(ctx, u.ID, 0, time.Time{}) },
			func() error { return tx.RevokeUserSessions(ctx, u.ID, p.SessionID, now) },
		} {
			if err := f(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if fail != nil {
		return fail
	}
	a.event(ctx, slog.LevelInfo, "password_changed", "user_id", u.ID, "session_id", p.SessionID)
	return nil
}

// RegenerateRecoveryCodes replaces the caller's recovery codes, given a TOTP code.
func (a *Auth) RegenerateRecoveryCodes(ctx context.Context, p Principal, code string) ([]string, error) {
	now := a.now()
	var codes []string
	var fail error
	err := a.users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error {
		u, err := tx.UserByID(ctx, p.UserID)
		if err != nil {
			return err
		}
		var step int64
		if step, fail, err = a.reauth(ctx, tx, u, code, now); err != nil || fail != nil {
			return err
		}
		if err := tx.SetUserTOTPStep(ctx, u.ID, step); err != nil {
			return err
		}
		codes, err = a.newRecoveryCodes(ctx, tx, u.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	if fail != nil {
		return nil, fail
	}
	a.event(ctx, slog.LevelInfo, "recovery_codes_regenerated", "user_id", p.UserID)
	return codes, nil
}

// UserSessions returns a user's active sessions, newest first (store.ErrNoUser when there is
// no such user).
func (a *Auth) UserSessions(ctx context.Context, userID int64) ([]model.Session, error) {
	now := a.now()
	return store.ReadUsersValue(ctx, a.users, func(ctx context.Context, tx *store.UsersReadTx) ([]model.Session, error) {
		if _, err := tx.UserByID(ctx, userID); err != nil {
			return nil, err
		}
		return tx.UserSessions(ctx, userID, now)
	})
}

// RevokeUserSession revokes one of the user's own sessions. Another user's session is
// store.ErrNoSession, exactly like a missing one (OWASP API1).
func (a *Auth) RevokeUserSession(ctx context.Context, userID, sessionID int64) error {
	err := a.users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error {
		return tx.RevokeUserSession(ctx, userID, sessionID, a.now())
	})
	if err == nil {
		a.event(ctx, slog.LevelInfo, "session_revoked", "user_id", userID, "session_id", sessionID, "by", "user")
	}
	return err
}
