package handlers

import (
	"errors"
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/convert"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

// The signed-in user's own account (stage 16f). Every route acts only on the account and
// sessions of runtime.Principal, which the public router's token check sets (on the admin
// socket these routes are always 401).

// Logout is POST /api/v1/auth/logout (204): ends the access token's session; its refresh token
// stops working and so does the access token.
func (a *Handlers) Logout(r *http.Request) (any, error) {
	p, err := principalNoParams(r)
	if err != nil {
		return nil, err
	}
	if err := a.ctl.Auth().Logout(r.Context(), p); err != nil {
		return nil, err
	}
	return runtime.NoContent(), nil
}

// Account is GET /api/v1/account: the signed-in user's account.
func (a *Handlers) Account(r *http.Request) (any, error) {
	p, err := principalNoParams(r)
	if err != nil {
		return nil, err
	}
	u, err := a.ctl.Auth().Account(r.Context(), p.UserID)
	if err != nil {
		return nil, err
	}
	return convert.Account(u), nil
}

// ChangePassword is PUT /api/v1/account/password, body {current_password, password, code}
// (204): replaces the password and revokes every other session.
func (a *Handlers) ChangePassword(r *http.Request) (any, error) {
	p, err := runtime.Principal(r)
	if err != nil {
		return nil, err
	}
	var req request.ChangePassword
	if err := decodeBody(r, &req); err != nil {
		return nil, err
	}
	err = a.ctl.Auth().ChangePassword(r.Context(), p, req.CurrentPassword, req.Password, req.Code)
	if err != nil {
		return nil, reauthError(err)
	}
	return runtime.NoContent(), nil
}

// RegenerateRecoveryCodes is POST /api/v1/account/recovery-codes, body {code}: new recovery
// codes, shown only here; the old ones stop working.
func (a *Handlers) RegenerateRecoveryCodes(r *http.Request) (any, error) {
	p, err := runtime.Principal(r)
	if err != nil {
		return nil, err
	}
	var req request.RegenerateRecoveryCodes
	if err := decodeBody(r, &req); err != nil {
		return nil, err
	}
	codes, err := a.ctl.Auth().RegenerateRecoveryCodes(r.Context(), p, req.Code)
	if err != nil {
		return nil, reauthError(err)
	}
	return convert.RecoveryCodes(codes), nil
}

// AccountSessions is GET /api/v1/account/sessions: the signed-in user's active sessions,
// newest first.
func (a *Handlers) AccountSessions(r *http.Request) (any, error) {
	p, err := principalNoParams(r)
	if err != nil {
		return nil, err
	}
	items, err := a.ctl.Auth().UserSessions(r.Context(), p.UserID)
	if err != nil {
		return nil, err
	}
	return convert.SessionList(items), nil
}

// RevokeAccountSession is DELETE /api/v1/account/sessions/{id} (204): ends one of the user's
// own sessions. Another user's session is 404, exactly like a missing one (OWASP API1).
func (a *Handlers) RevokeAccountSession(r *http.Request) (any, error) {
	p, err := runtime.Principal(r)
	if err != nil {
		return nil, err
	}
	id, err := runtime.OnlyPathID(r, dto.ParamID)
	if err != nil {
		return nil, err
	}
	if err := a.ctl.Auth().RevokeUserSession(r.Context(), p.UserID, id); err != nil {
		return nil, err
	}
	return runtime.NoContent(), nil
}

// principalNoParams is the signed-in user of a route that takes no parameters.
func principalNoParams(r *http.Request) (controllers.Principal, error) {
	p, err := runtime.Principal(r)
	if err != nil {
		return p, err
	}
	return p, runtime.NoParams(r)
}

// reauthError makes a wrong current password or TOTP code on a signed-in change 403 forbidden:
// the access token was fine, so a 401 (which tells the client to refresh or log in again, RFC
// 6750 §3.1) would be wrong.
func reauthError(err error) error {
	if errors.Is(err, controllers.ErrInvalidCredentials) {
		return problem.WithDetail(dto.TypeForbidden, runtime.DetailReauthCredentials)
	}
	return err
}
