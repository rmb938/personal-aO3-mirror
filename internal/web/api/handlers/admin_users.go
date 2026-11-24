package handlers

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/convert"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

// The admin socket's account routes (stage 16). A missing invite, account or session is 404.

// CreateInvite is POST /api/v1/admin/invites, body {ttl?}: a signup invite (201). Its link is
// in the body only; nothing else ever shows the token.
func (a *Handlers) CreateInvite(r *http.Request) (any, error) {
	q, err := runtime.ParseParams(r)
	if err != nil {
		return nil, err
	}
	var req request.CreateInvite
	if err := runtime.DecodeJSON(r, &req, &q.Validation); err != nil {
		return nil, err
	}
	if err := q.Err(); err != nil {
		return nil, err
	}
	l, err := a.ctl.Auth().CreateInvite(r.Context(), req.TTL.Duration())
	if err != nil {
		return nil, err
	}
	return &runtime.Response{Status: http.StatusCreated, Body: convert.InviteLink(l)}, nil
}

// ListInvites is GET /api/v1/admin/invites: every invite, newest first.
func (a *Handlers) ListInvites(r *http.Request) (any, error) {
	if err := runtime.NoParams(r); err != nil {
		return nil, err
	}
	items, err := a.ctl.Auth().ListInvites(r.Context())
	if err != nil {
		return nil, err
	}
	return convert.InviteList(items), nil
}

// ListUsers is GET /api/v1/admin/users: every account, by ID.
func (a *Handlers) ListUsers(r *http.Request) (any, error) {
	if err := runtime.NoParams(r); err != nil {
		return nil, err
	}
	items, err := a.ctl.Auth().ListUsers(r.Context())
	if err != nil {
		return nil, err
	}
	return convert.UserInfoList(items), nil
}

// CreateReset is POST /api/v1/admin/users/{id}/reset, body {scope, ttl?}: a reset link for
// the account's password, TOTP secret or both (201).
func (a *Handlers) CreateReset(r *http.Request) (any, error) {
	q, err := runtime.ParseParams(r)
	if err != nil {
		return nil, err
	}
	id := q.PathID(dto.ParamID)
	var req request.CreateReset
	if err := runtime.DecodeJSON(r, &req, &q.Validation); err != nil {
		return nil, err
	}
	if err := q.Err(); err != nil {
		return nil, err
	}
	l, err := a.ctl.Auth().CreateReset(r.Context(), id, convert.ResetScope(req), req.TTL.Duration())
	if err != nil {
		return nil, err
	}
	return &runtime.Response{Status: http.StatusCreated, Body: convert.InviteLink(l)}, nil
}

// UserSessions is GET /api/v1/admin/users/{id}/sessions: the account's active sessions,
// newest first.
func (a *Handlers) UserSessions(r *http.Request) (any, error) {
	id, err := runtime.OnlyPathID(r, dto.ParamID)
	if err != nil {
		return nil, err
	}
	items, err := a.ctl.Auth().UserSessions(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.SessionList(items), nil
}

// RotateSigningKey is POST /api/v1/admin/signing-keys/rotate (204): a new access-token signing
// key; tokens signed by the old one keep verifying until they expire.
func (a *Handlers) RotateSigningKey(r *http.Request) (any, error) {
	if err := runtime.NoParams(r); err != nil {
		return nil, err
	}
	if _, err := a.ctl.Auth().RotateSigningKey(r.Context()); err != nil {
		return nil, err
	}
	return runtime.NoContent(), nil
}

// RotateMasterKey is POST /api/v1/admin/master-keys/rotate (204): a new master key, with every
// secret in users.db re-encrypted under it before the response. The retired key is kept for 7
// days (docs/part3-key-rotation.md "Rotation").
func (a *Handlers) RotateMasterKey(r *http.Request) (any, error) {
	if err := runtime.NoParams(r); err != nil {
		return nil, err
	}
	if _, err := a.ctl.Auth().MasterKeys().RotateMasterKey(r.Context()); err != nil {
		return nil, err
	}
	return runtime.NoContent(), nil
}
