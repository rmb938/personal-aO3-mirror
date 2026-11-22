package controllers

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// The admin socket's account operations. A missing user, invite or session is
// store.ErrNoUser, store.ErrNoInvite or store.ErrNoSession.

// InviteLink is a new invite: its ID and the link to hand to the person (shown once; the
// token is in the fragment, so it never reaches a proxy or server log).
type InviteLink struct {
	ID        int64
	URL       string
	ExpiresAt time.Time
}

// inviteTTL applies the default and the bounds.
func (a *Auth) inviteTTL(ttl time.Duration) (time.Duration, error) {
	if ttl == 0 {
		ttl = a.opts.InviteTTL
	}
	if ttl < MinInviteTTL || ttl > MaxInviteTTL {
		return 0, ErrInviteTTL
	}
	return ttl, nil
}

func (a *Auth) createInvite(ctx context.Context, inv model.Invite, ttl time.Duration, page string) (InviteLink, error) {
	ttl, err := a.inviteTTL(ttl)
	if err != nil {
		return InviteLink{}, err
	}
	now := a.now()
	inv.CreatedAt, inv.ExpiresAt = now, now.Add(ttl)
	token, sum := auth.NewToken(auth.InviteToken)
	id, err := store.WriteUsersValue(ctx, a.users, func(ctx context.Context, tx *store.UsersTx) (int64, error) {
		if inv.TargetUserID != 0 {
			if _, err := tx.UserByID(ctx, inv.TargetUserID); err != nil {
				return 0, err
			}
		}
		return tx.CreateInvite(ctx, inv, sum)
	})
	if err != nil {
		return InviteLink{}, err
	}
	a.event(ctx, slog.LevelInfo, "invite_created", "invite_id", id, "kind", string(inv.Kind),
		"user_id", inv.TargetUserID, "scope", string(inv.Scope), "ttl_s", int64(ttl.Seconds()))
	return InviteLink{ID: id, URL: strings.TrimSuffix(a.opts.PublicURL, "/") + "/" + page + "#token=" + token,
		ExpiresAt: inv.ExpiresAt}, nil
}

// CreateInvite makes a signup invite valid for ttl (0 = auth.invite_ttl; at most
// MaxInviteTTL).
func (a *Auth) CreateInvite(ctx context.Context, ttl time.Duration) (InviteLink, error) {
	return a.createInvite(ctx, model.Invite{Kind: model.InviteSignup}, ttl, "signup")
}

// CreateReset makes a reset link for a user's password, TOTP secret or both.
func (a *Auth) CreateReset(ctx context.Context, userID int64, scope model.ResetScope, ttl time.Duration) (InviteLink, error) {
	if !slices.Contains(model.ResetScopes(), scope) {
		return InviteLink{}, ErrResetScope
	}
	return a.createInvite(ctx, model.Invite{Kind: model.InviteReset, TargetUserID: userID, Scope: scope}, ttl, "reset")
}

// ListInvites returns every invite, newest first.
func (a *Auth) ListInvites(ctx context.Context) ([]model.Invite, error) {
	return store.ReadUsersValue(ctx, a.users, func(ctx context.Context, tx *store.UsersReadTx) ([]model.Invite, error) {
		return tx.ListInvites(ctx)
	})
}

// RevokeInvite revokes an unused invite and drops its pending enrollment (a used or revoked
// invite is left as it is).
func (a *Auth) RevokeInvite(ctx context.Context, id int64) error {
	err := a.users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error {
		return tx.RevokeInvite(ctx, id, a.now())
	})
	if err == nil {
		a.event(ctx, slog.LevelInfo, "invite_revoked", "invite_id", id)
	}
	return err
}

// ListUsers returns every account by ID.
func (a *Auth) ListUsers(ctx context.Context) ([]UserInfo, error) {
	return store.ReadUsersValue(ctx, a.users, func(ctx context.Context, tx *store.UsersReadTx) ([]UserInfo, error) {
		users, err := tx.ListUsers(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]UserInfo, len(users))
		for i, u := range users {
			n, err := tx.RecoveryCodesLeft(ctx, u.ID)
			if err != nil {
				return nil, err
			}
			out[i] = userInfo(u, n)
		}
		return out, nil
	})
}

// DisableUser disables an account and revokes its sessions.
func (a *Auth) DisableUser(ctx context.Context, id int64) error {
	now := a.now()
	err := a.users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error {
		if err := tx.DisableUser(ctx, id, now); err != nil {
			return err
		}
		return tx.RevokeUserSessions(ctx, id, 0, now)
	})
	if err == nil {
		a.event(ctx, slog.LevelInfo, "account_disabled", "user_id", id, "reason", "admin")
	}
	return err
}

// EnableUser enables an account and clears its failures and backoff.
func (a *Auth) EnableUser(ctx context.Context, id int64) error {
	err := a.users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error { return tx.EnableUser(ctx, id) })
	if err == nil {
		a.event(ctx, slog.LevelInfo, "account_enabled", "user_id", id)
	}
	return err
}

// DeleteUser deletes an account with its sessions, recovery codes and reset invites.
func (a *Auth) DeleteUser(ctx context.Context, id int64) error {
	err := a.users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error { return tx.DeleteUser(ctx, id) })
	if err == nil {
		a.event(ctx, slog.LevelInfo, "account_deleted", "user_id", id)
	}
	return err
}

// RevokeSession revokes any session (the admin socket's; users go through RevokeUserSession).
func (a *Auth) RevokeSession(ctx context.Context, sessionID int64) error {
	err := a.users.InTx(ctx, func(ctx context.Context, tx *store.UsersTx) error {
		return tx.RevokeSession(ctx, sessionID, a.now())
	})
	if err == nil {
		a.event(ctx, slog.LevelInfo, "session_revoked", "session_id", sessionID, "by", "admin")
	}
	return err
}
