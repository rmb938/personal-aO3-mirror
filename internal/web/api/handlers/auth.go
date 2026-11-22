package handlers

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/convert"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

// The auth entry points (stage 16f): no access token, every token in the body (never the URL,
// so none reaches a log). A bad token or code is 401 invalid-credentials; a refused username
// or password is 422 on its member.

// Signup is POST /api/v1/auth/signup, body {invite_token, username, password}: a new TOTP
// secret and the enrollment token to confirm it with.
func (a *Handlers) Signup(r *http.Request) (any, error) {
	var req request.Signup
	if err := decodeBody(r, &req); err != nil {
		return nil, err
	}
	e, err := a.ctl.Auth().Signup(r.Context(), req.InviteToken, req.Username, req.Password)
	if err != nil {
		return nil, err
	}
	return convert.Enrollment(e), nil
}

// ConfirmSignup is POST /api/v1/auth/signup/confirm, body {enrollment_token, code}: creates the
// account and returns its recovery codes, shown only here. The user then logs in.
func (a *Handlers) ConfirmSignup(r *http.Request) (any, error) {
	var req request.ConfirmEnrollment
	if err := decodeBody(r, &req); err != nil {
		return nil, err
	}
	c, err := a.ctl.Auth().ConfirmSignup(r.Context(), req.EnrollmentToken, req.Code)
	if err != nil {
		return nil, err
	}
	return &runtime.Response{Status: http.StatusCreated, Body: convert.Confirmed(c)}, nil
}

// StartReset is POST /api/v1/auth/reset, body {invite_token, password?}: the enrollment token
// and, when the reset replaces the TOTP secret, the new one.
func (a *Handlers) StartReset(r *http.Request) (any, error) {
	var req request.Reset
	if err := decodeBody(r, &req); err != nil {
		return nil, err
	}
	e, err := a.ctl.Auth().StartReset(r.Context(), req.InviteToken, req.Password)
	if err != nil {
		return nil, err
	}
	return convert.Enrollment(e), nil
}

// ConfirmReset is POST /api/v1/auth/reset/confirm, body {enrollment_token, code}: applies the
// reset and revokes the account's sessions. The body holds new recovery codes when the TOTP
// secret was replaced, else an empty list.
func (a *Handlers) ConfirmReset(r *http.Request) (any, error) {
	var req request.ConfirmEnrollment
	if err := decodeBody(r, &req); err != nil {
		return nil, err
	}
	c, err := a.ctl.Auth().ConfirmReset(r.Context(), req.EnrollmentToken, req.Code)
	if err != nil {
		return nil, err
	}
	return convert.Confirmed(c), nil
}

// decodeBody reads a route with no query parameters and a JSON body: it returns every invalid
// parameter and member at once.
func decodeBody(r *http.Request, req request.Request) error {
	q, err := runtime.ParseParams(r)
	if err != nil {
		return err
	}
	if err := runtime.DecodeJSON(r, req, &q.Validation); err != nil {
		return err
	}
	return q.Err()
}
