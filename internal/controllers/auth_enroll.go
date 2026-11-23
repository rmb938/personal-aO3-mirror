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

// Enrollment is a signup's or reset's first step passed: confirm Token with a TOTP code before
// ExpiresAt. TOTPSecret (base32) and OTPAuthURI are set when there is a new TOTP secret to
// enrol in an authenticator app.
type Enrollment struct {
	Token      string
	TOTPSecret string
	OTPAuthURI string
	ExpiresAt  time.Time
}

// Confirmed is a completed signup or reset. The recovery codes (none for a password-only
// reset) are shown once.
type Confirmed struct {
	UserID        int64
	RecoveryCodes []string
}

// inviteFor returns the usable invite of kind whose token hashes to sum, or why not.
func inviteFor(ctx context.Context, tx *store.UsersReadTx, sum [32]byte, kind model.InviteKind, now time.Time) (model.Invite, string, error) {
	inv, err := tx.InviteByToken(ctx, sum)
	if errors.Is(err, store.ErrNoInvite) {
		return inv, reasonUnknownToken, nil
	} else if err != nil {
		return inv, "", err
	}
	return inv, inviteRefusal(inv, kind, now), nil
}

// inviteRefusal is why inv can't be used as kind at now, or "".
func inviteRefusal(inv model.Invite, kind model.InviteKind, now time.Time) string {
	switch {
	case inv.Kind != kind:
		return reasonWrongKind
	case !inv.UsedAt.IsZero() || !inv.RevokedAt.IsZero():
		return reasonSpent
	case !now.Before(inv.ExpiresAt):
		return reasonExpired
	}
	return ""
}

// enrollmentExpiry is when a pending enrollment started at now ends: EnrollmentTTL, never past
// its invite.
func enrollmentExpiry(inv model.Invite, now time.Time) time.Time {
	if t := now.Add(EnrollmentTTL); t.Before(inv.ExpiresAt) {
		return t
	}
	return inv.ExpiresAt
}

// Signup is a signup's first step: an invite, a username and a password. It checks the invite,
// then the username (ErrInvalidUsername, ErrUsernameTaken) and the password policy (the
// auth.ErrPassword* errors), and returns a new TOTP secret to confirm with ConfirmSignup. A
// second Signup on the same invite replaces the first.
func (a *Auth) Signup(ctx context.Context, inviteToken, username, password string) (Enrollment, error) {
	isum, ok := auth.TokenSum(auth.InviteToken, inviteToken)
	if !ok {
		return Enrollment{}, ErrInvalidToken
	}
	now := a.now()
	check := func(ctx context.Context, tx *store.UsersReadTx, name string) (model.Invite, error) {
		inv, reason, err := inviteFor(ctx, tx, isum, model.InviteSignup, now)
		if err != nil {
			return inv, err
		}
		if reason != "" {
			a.event(ctx, slog.LevelInfo, "signup_refused", "reason", reason, "invite_id", inv.ID)
			return inv, ErrInvalidToken
		}
		if name == "" {
			return inv, nil
		}
		taken, err := tx.UsernameTaken(ctx, name)
		if err != nil {
			return inv, err
		}
		if taken {
			return inv, ErrUsernameTaken
		}
		return inv, nil
	}
	// The invite first, so nothing about usernames is said without one.
	if _, err := store.ReadUsersValue(ctx, a.users, func(ctx context.Context, tx *store.UsersReadTx) (model.Invite, error) {
		return check(ctx, tx, "")
	}); err != nil {
		return Enrollment{}, err
	}
	name, valid := NormalizeUsername(username)
	if !valid {
		return Enrollment{}, ErrInvalidUsername
	}
	if err := auth.CheckPassword(password, name); err != nil {
		return Enrollment{}, err
	}
	hashedFor, err := store.ReadUsersValue(ctx, a.users, func(ctx context.Context, tx *store.UsersReadTx) (model.Invite, error) {
		return check(ctx, tx, name)
	})
	if err != nil {
		return Enrollment{}, err
	}
	phc, err := a.hashPassword(ctx, auth.EnrollmentPassword(hashedFor.ID), password)
	if err != nil {
		return Enrollment{}, err
	}

	secret := auth.NewTOTPSecret()
	token, esum := auth.NewToken(auth.EnrollmentToken)
	inv, err := store.WriteUsersValue(ctx, a.users, func(ctx context.Context, tx *store.UsersTx) (model.Invite, error) {
		inv, err := check(ctx, &tx.UsersReadTx, name) // again: the hash took time
		if err != nil {
			return inv, err
		}
		if inv.ID != hashedFor.ID { // the same token names the same invite
			return inv, errors.New("controllers: signup invite changed during the hash")
		}
		if err := tx.DeleteExpiredEnrollments(ctx, now); err != nil {
			return inv, err
		}
		return inv, tx.PutEnrollment(ctx, model.Enrollment{
			InviteID: inv.ID, Username: name, PasswordPHC: phc,
			TOTPSecretEnc: a.opts.Keyring.Load().EncryptTOTPSecret(auth.EnrollmentTOTP(inv.ID), secret),
			ExpiresAt:     enrollmentExpiry(inv, now),
		}, esum)
	})
	if err != nil {
		return Enrollment{}, err
	}
	a.event(ctx, slog.LevelInfo, "signup_started", "invite_id", inv.ID)
	return Enrollment{Token: token, TOTPSecret: auth.EncodeTOTPSecret(secret),
		OTPAuthURI: auth.TOTPURI(a.opts.TOTPIssuer, name, secret), ExpiresAt: enrollmentExpiry(inv, now)}, nil
}

// pendingEnrollment returns the unexpired enrollment whose token hashes to sum and its invite,
// usable as kind, or a refusal reason.
func pendingEnrollment(ctx context.Context, tx *store.UsersTx, sum [32]byte, kind model.InviteKind, now time.Time) (model.Enrollment, model.Invite, string, error) {
	e, err := tx.EnrollmentByToken(ctx, sum)
	if errors.Is(err, store.ErrNoEnrollment) {
		return e, model.Invite{}, reasonUnknownToken, nil
	} else if err != nil {
		return e, model.Invite{}, "", err
	}
	if !now.Before(e.ExpiresAt) {
		return e, model.Invite{}, reasonExpired, tx.DeleteEnrollment(ctx, e.InviteID)
	}
	inv, err := tx.InviteByID(ctx, e.InviteID)
	if err != nil {
		return e, inv, "", err // the enrollment is deleted with its invite
	}
	return e, inv, inviteRefusal(inv, kind, now), nil
}

// ConfirmSignup is a signup's second step: the enrollment token and a code from the new TOTP
// secret. In one transaction it re-checks the invite and username, creates the account,
// consumes the invite and returns the recovery codes. The user then logs in.
func (a *Auth) ConfirmSignup(ctx context.Context, enrollmentToken, code string) (Confirmed, error) {
	esum, ok := auth.TokenSum(auth.EnrollmentToken, enrollmentToken)
	if !ok {
		return Confirmed{}, ErrInvalidToken
	}
	now := a.now()
	var out Confirmed
	var inviteID int64
	var reason string
	var fail error
	err := a.users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error {
		e, inv, why, err := pendingEnrollment(ctx, tx, esum, model.InviteSignup, now)
		inviteID = e.InviteID
		if err != nil || why != "" {
			reason, fail = why, ErrInvalidToken
			return err
		}
		secret, err := a.opts.Keyring.Load().DecryptTOTPSecret(auth.EnrollmentTOTP(inv.ID), e.TOTPSecretEnc)
		if err != nil {
			return err
		}
		step, ok := auth.VerifyTOTP(secret, code, now, 0)
		if !ok {
			reason, fail = reasonBadCode, ErrInvalidCredentials
			return nil
		}
		taken, err := tx.UsernameTaken(ctx, e.Username)
		if err != nil {
			return err
		}
		if taken {
			fail = ErrUsernameTaken
			return nil
		}
		// The secret and password hash are sealed for the user's ID, which exists only after the
		// insert.
		uid, err := tx.CreateUser(ctx, model.User{Username: e.Username, PasswordPHC: "",
			TOTPSecretEnc: []byte{}, CreatedAt: now, PasswordChangedAt: now})
		if err != nil {
			return err
		}
		phc, err := a.opts.Keyring.Load().RewrapPasswordHash(auth.EnrollmentPassword(inv.ID), auth.UserPassword(uid), e.PasswordPHC)
		if err != nil {
			return err
		}
		if err := tx.SetUserPassword(ctx, uid, phc, now); err != nil {
			return err
		}
		if err := tx.SetUserTOTP(ctx, uid, a.opts.Keyring.Load().EncryptTOTPSecret(auth.UserTOTP(uid), secret), step); err != nil {
			return err
		}
		codes, err := a.newRecoveryCodes(ctx, tx, uid)
		if err != nil {
			return err
		}
		if err := consumeInvite(ctx, tx, inv.ID, uid, now); err != nil {
			return err
		}
		out = Confirmed{UserID: uid, RecoveryCodes: codes}
		return nil
	})
	if err != nil {
		return Confirmed{}, err
	}
	if fail != nil {
		if reason != "" {
			a.event(ctx, slog.LevelInfo, "signup_refused", "reason", reason, "invite_id", inviteID, "step", "confirm")
		}
		return Confirmed{}, fail
	}
	a.event(ctx, slog.LevelInfo, "signup_completed", "user_id", out.UserID, "invite_id", inviteID)
	return out, nil
}

// consumeInvite marks an invite used by uid and deletes its enrollment.
func consumeInvite(ctx context.Context, tx *store.UsersTx, inviteID, uid int64, now time.Time) error {
	used, err := tx.UseInvite(ctx, inviteID, uid, now)
	if err != nil {
		return err
	}
	if !used {
		return errors.New("controllers: invite used during confirm") // checked above in this tx
	}
	return tx.DeleteEnrollment(ctx, inviteID)
}

// StartReset is a reset's first step: a reset invite and, when its scope replaces the password,
// the new password (ErrResetPassword when that doesn't match). When the scope replaces the TOTP
// secret it returns a new one; otherwise ConfirmReset takes a code from the current one.
func (a *Auth) StartReset(ctx context.Context, inviteToken, password string) (Enrollment, error) {
	isum, ok := auth.TokenSum(auth.InviteToken, inviteToken)
	if !ok {
		return Enrollment{}, ErrInvalidToken
	}
	now := a.now()
	check := func(ctx context.Context, tx *store.UsersReadTx) (resetTarget, error) {
		inv, reason, err := inviteFor(ctx, tx, isum, model.InviteReset, now)
		if err != nil {
			return resetTarget{}, err
		}
		if reason != "" {
			a.event(ctx, slog.LevelInfo, "reset_refused", "reason", reason, "invite_id", inv.ID)
			return resetTarget{}, ErrInvalidToken
		}
		u, err := tx.UserByID(ctx, inv.TargetUserID)
		return resetTarget{inv, u}, err // the invite is deleted with its user, so not ErrNoUser
	}
	t, err := store.ReadUsersValue(ctx, a.users, check)
	if err != nil {
		return Enrollment{}, err
	}
	if t.inv.Scope.Password() != (password != "") {
		return Enrollment{}, ErrResetPassword
	}
	phc := ""
	if password != "" {
		if err := auth.CheckPassword(password, t.user.Username); err != nil {
			return Enrollment{}, err
		}
		if phc, err = a.hashPassword(ctx, auth.EnrollmentPassword(t.inv.ID), password); err != nil {
			return Enrollment{}, err
		}
	}
	hashedFor := t.inv.ID

	var secret []byte
	if t.inv.Scope.TOTP() {
		secret = auth.NewTOTPSecret()
	}
	token, esum := auth.NewToken(auth.EnrollmentToken)
	t, err = store.WriteUsersValue(ctx, a.users, func(ctx context.Context, tx *store.UsersTx) (resetTarget, error) {
		t, err := check(ctx, &tx.UsersReadTx)
		if err != nil {
			return t, err
		}
		if t.inv.ID != hashedFor { // the same token names the same invite
			return t, errors.New("controllers: reset invite changed during the hash")
		}
		if err := tx.DeleteExpiredEnrollments(ctx, now); err != nil {
			return t, err
		}
		e := model.Enrollment{InviteID: t.inv.ID, PasswordPHC: phc, ExpiresAt: enrollmentExpiry(t.inv, now)}
		if secret != nil {
			e.TOTPSecretEnc = a.opts.Keyring.Load().EncryptTOTPSecret(auth.EnrollmentTOTP(t.inv.ID), secret)
		}
		return t, tx.PutEnrollment(ctx, e, esum)
	})
	if err != nil {
		return Enrollment{}, err
	}
	a.event(ctx, slog.LevelInfo, "reset_started", "invite_id", t.inv.ID, "user_id", t.user.ID,
		"scope", string(t.inv.Scope))
	out := Enrollment{Token: token, ExpiresAt: enrollmentExpiry(t.inv, now)}
	if secret != nil {
		out.TOTPSecret = auth.EncodeTOTPSecret(secret)
		out.OTPAuthURI = auth.TOTPURI(a.opts.TOTPIssuer, t.user.Username, secret)
	}
	return out, nil
}

type resetTarget struct {
	inv  model.Invite
	user model.User
}

// ConfirmReset is a reset's second step: the enrollment token and a TOTP code (from the new
// secret when the scope replaces it, else from the current one, which is checked like a
// re-authentication: during the account's backoff it is RateLimitedError, and a wrong code
// counts against the account). In one transaction it applies
// the new password and/or secret, clears the backoff, revokes every session of the user and
// consumes the invite. Only a scope that replaces the TOTP secret replaces the recovery codes
// (returned once); a password-only reset leaves them and returns none.
func (a *Auth) ConfirmReset(ctx context.Context, enrollmentToken, code string) (Confirmed, error) {
	esum, ok := auth.TokenSum(auth.EnrollmentToken, enrollmentToken)
	if !ok {
		return Confirmed{}, ErrInvalidToken
	}
	now := a.now()
	var out Confirmed
	var inviteID int64
	var reason string
	var fail error
	err := a.users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error {
		e, inv, why, err := pendingEnrollment(ctx, tx, esum, model.InviteReset, now)
		inviteID = e.InviteID
		if err != nil || why != "" {
			reason, fail = why, ErrInvalidToken
			return err
		}
		u, err := tx.UserByID(ctx, inv.TargetUserID)
		if err != nil {
			return err
		}
		newSecret := e.TOTPSecretEnc != nil
		if newSecret {
			secret, err := a.opts.Keyring.Load().DecryptTOTPSecret(auth.EnrollmentTOTP(inv.ID), e.TOTPSecretEnc)
			if err != nil {
				return err
			}
			step, ok := auth.VerifyTOTP(secret, code, now, 0)
			if !ok {
				reason, fail = reasonBadCode, ErrInvalidCredentials
				return nil
			}
			if err := tx.SetUserTOTP(ctx, u.ID, a.opts.Keyring.Load().EncryptTOTPSecret(auth.UserTOTP(u.ID), secret), step); err != nil {
				return err
			}
		} else {
			// The code is the account's own second factor, so it is guessed against the account:
			// the backoff applies and a wrong code counts like a failed login (NIST SP 800-63B-4
			// §3.2.2). Otherwise a leaked password-only reset link would allow TOTP guesses
			// limited only per client.
			step, f, err := a.reauth(ctx, tx, u, code, now)
			if err != nil {
				return err
			}
			if f != nil {
				reason, fail = reasonBadCode, f
				if errors.As(f, new(RateLimitedError)) {
					reason = reasonBackoff
				}
				return nil
			}
			if err := tx.SetUserTOTPStep(ctx, u.ID, step); err != nil {
				return err
			}
		}
		if e.PasswordPHC != "" {
			phc, err := a.opts.Keyring.Load().RewrapPasswordHash(auth.EnrollmentPassword(inv.ID), auth.UserPassword(u.ID), e.PasswordPHC)
			if err != nil {
				return err
			}
			if err := tx.SetUserPassword(ctx, u.ID, phc, now); err != nil {
				return err
			}
		}
		var codes []string
		if newSecret { // 2FA re-enrolled: new codes too (the old ones may be lost or used up)
			if codes, err = a.newRecoveryCodes(ctx, tx, u.ID); err != nil {
				return err
			}
		}
		if err := tx.SetUserAttempts(ctx, u.ID, 0, time.Time{}); err != nil {
			return err
		}
		if err := tx.RevokeUserSessions(ctx, u.ID, 0, now); err != nil {
			return err
		}
		if err := consumeInvite(ctx, tx, inv.ID, u.ID, now); err != nil {
			return err
		}
		out = Confirmed{UserID: u.ID, RecoveryCodes: codes}
		return nil
	})
	if err != nil {
		return Confirmed{}, err
	}
	if fail != nil {
		a.event(ctx, slog.LevelInfo, "reset_refused", "reason", reason, "invite_id", inviteID, "step", "confirm")
		return Confirmed{}, fail
	}
	a.event(ctx, slog.LevelInfo, "reset_completed", "user_id", out.UserID, "invite_id", inviteID)
	return out, nil
}
