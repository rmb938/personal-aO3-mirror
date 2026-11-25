package handlers

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/convert"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
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

// Login is POST /api/v1/auth/login, body {username, password}: a challenge token for the
// second factor. An unknown username, a wrong password and a disabled account are the same
// 401; too many failures are 429 with Retry-After.
func (a *Handlers) Login(r *http.Request) (any, error) {
	var req request.Login
	if err := decodeBody(r, &req); err != nil {
		return nil, err
	}
	c, err := a.ctl.Auth().Login(r.Context(), req.Username, req.Password)
	if err != nil {
		return nil, err
	}
	return convert.LoginChallenge(c), nil
}

// LoginSecondFactor is POST /api/v1/auth/login/totp, body {challenge_token, code |
// recovery_code, transport?}: starts a session and returns its access and refresh tokens (in
// the body, or with transport cookie as cookies).
func (a *Handlers) LoginSecondFactor(r *http.Request) (any, error) {
	var req request.LoginTOTP
	if err := decodeBody(r, &req); err != nil {
		return nil, err
	}
	cookie := req.Transport == dto.TransportCookie
	if cookie {
		if err := runtime.CheckCSRF(r); err != nil { // before the challenge spends a try
			return nil, err
		}
		runtime.UseCookieTransport(r)
	}
	p, err := a.ctl.Auth().LoginSecondFactor(r.Context(), req.ChallengeToken,
		controllers.SecondFactor{Code: req.Code, RecoveryCode: req.RecoveryCode})
	if err != nil {
		return nil, err
	}
	return tokens(p, cookie), nil
}

// Refresh is POST /api/v1/auth/refresh, body {refresh_token} or {transport: cookie} (the
// refresh cookie then holds the token): a new token pair, in the body or as cookies like the
// login's; the old refresh token stops working.
func (a *Handlers) Refresh(r *http.Request) (any, error) {
	var req request.Refresh
	if err := decodeBody(r, &req); err != nil {
		return nil, err
	}
	token, cookie := req.RefreshToken, req.Transport == dto.TransportCookie
	if cookie {
		if err := runtime.CheckCSRF(r); err != nil {
			return nil, err
		}
		var err error
		if token, err = runtime.RefreshCookie(r); err != nil {
			return nil, err
		}
	}
	p, err := a.ctl.Auth().Refresh(r.Context(), token)
	if err != nil {
		return nil, err
	}
	return tokens(p, cookie), nil
}

// tokens is a login's or refresh's response: the token pair in the body, or with the cookie
// transport the tokens as cookies and only their expiry times in the body.
func tokens(p controllers.TokenPair, cookie bool) any {
	if !cookie {
		return convert.TokenPair(p)
	}
	return &runtime.Response{Status: http.StatusOK, Cookies: runtime.SessionCookies(p), Body: convert.CookieSession(p)}
}
