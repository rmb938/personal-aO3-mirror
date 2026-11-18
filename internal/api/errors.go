// Package api is the HTTP API served by `ao3mirror serve` (docs/part2-api.md). Handlers call
// controllers and return values; only respond writes JSON.
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Code is a problem code: the closed set of machine-readable error kinds the API returns.
type Code string

const (
	CodeBadRequest           Code = "bad_request"
	CodeInvalidParam         Code = "invalid_param"
	CodeUnknownParam         Code = "unknown_param"
	CodeDuplicateParam       Code = "duplicate_param"
	CodeInvalidBody          Code = "invalid_body"
	CodeBodyTooLarge         Code = "body_too_large"
	CodeUnsupportedMediaType Code = "unsupported_media_type"
	CodeRevealRequired       Code = "reveal_required"
	CodeRevealDisabled       Code = "reveal_disabled"
	CodeNotFound             Code = "not_found"
	CodeMethodNotAllowed     Code = "method_not_allowed"
	CodeConflict             Code = "conflict"
	CodeExists               Code = "exists"
	CodeURITooLong           Code = "uri_too_long"
	CodeRateLimited          Code = "rate_limited"
	CodeUnavailable          Code = "unavailable"
	CodeInternal             Code = "internal"
)

// codeInfo is the fixed status and title for each code. The titles are registered as the
// `title` enum, so they pass the enforcer unhashed.
var codeInfo = map[Code]struct {
	status int
	title  string
}{
	CodeBadRequest:           {http.StatusBadRequest, "Bad request"},
	CodeInvalidParam:         {http.StatusBadRequest, "Invalid parameter"},
	CodeUnknownParam:         {http.StatusBadRequest, "Unknown parameter"},
	CodeDuplicateParam:       {http.StatusBadRequest, "Duplicate parameter"},
	CodeInvalidBody:          {http.StatusBadRequest, "Invalid request body"},
	CodeBodyTooLarge:         {http.StatusRequestEntityTooLarge, "Request body too large"},
	CodeUnsupportedMediaType: {http.StatusUnsupportedMediaType, "Unsupported media type"},
	CodeRevealRequired:       {http.StatusBadRequest, "Reveal required"},
	CodeRevealDisabled:       {http.StatusForbidden, "Reveal disabled on this server"},
	CodeNotFound:             {http.StatusNotFound, "Not found"},
	CodeMethodNotAllowed:     {http.StatusMethodNotAllowed, "Method not allowed"},
	CodeConflict:             {http.StatusConflict, "State conflict"},
	CodeExists:               {http.StatusConflict, "Already exists"},
	CodeURITooLong:           {http.StatusRequestURITooLong, "URI too long"},
	CodeRateLimited:          {http.StatusTooManyRequests, "Too many requests"},
	CodeUnavailable:          {http.StatusServiceUnavailable, "Service unavailable"},
	CodeInternal:             {http.StatusInternalServerError, "Internal server error"},
}

// knownFields are the request parameter and body field names a problem's `field` may name. A
// field not listed is dropped from the problem (fail safe), and the list is registered as the
// `field` enum. Add names as routes add parameters.
var knownFields = map[string]bool{
	"page":      true,
	"per_page":  true,
	"id":        true,
	RevealParam: true,
}

// Error is an API error with a fixed code, and optionally the parameter or field at fault.
// Handlers and helpers return it; respond turns it into a problem.
type Error struct {
	Code  Code
	Field string
	// Status overrides the code's default status when non-zero (e.g. a reveal_required that
	// replaces a non-JSON response is a 403).
	Status int
}

func (e *Error) Error() string {
	if e.Field != "" {
		return "api: " + string(e.Code) + " (" + e.Field + ")"
	}
	return "api: " + string(e.Code)
}

func errCode(c Code) *Error                { return &Error{Code: c} }
func errField(c Code, field string) *Error { return &Error{Code: c, Field: field} }

// problem builds the problem body for e. Unknown codes become internal; unknown fields are
// dropped.
func (e *Error) problem() dto.Problem {
	info, ok := codeInfo[e.Code]
	code := e.Code
	if !ok {
		code, info = CodeInternal, codeInfo[CodeInternal]
	}
	p := dto.Problem{Code: string(code), Title: info.title, Status: info.status}
	if ok && e.Status != 0 {
		p.Status = e.Status
	}
	if knownFields[e.Field] {
		p.Field = e.Field
	}
	return p
}

// problemFor maps any handler error to a problem, in one place: *Error as is; store
// not-found sentinels → 404; state conflicts → 409; an existing source → 409 exists; a
// cancelled or timed-out request → 503; anything else → 500 internal. internal reports
// whether the error is unexpected (the caller logs it).
func problemFor(err error) (p dto.Problem, internal bool) {
	if e, ok := errors.AsType[*Error](err); ok {
		p = e.problem()
		return p, p.Code == string(CodeInternal)
	}
	var code Code
	switch {
	case errors.Is(err, store.ErrNoSource), errors.Is(err, store.ErrNoSourceAdd),
		errors.Is(err, store.ErrNoJob), errors.Is(err, store.ErrNoWork),
		errors.Is(err, store.ErrNoSeries), errors.Is(err, store.ErrNoTag),
		errors.Is(err, store.ErrNoRawPage):
		code = CodeNotFound
	case errors.Is(err, store.ErrStateConflict):
		code = CodeConflict
	case isExists(err):
		code = CodeExists
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		code = CodeUnavailable
	default:
		return errCode(CodeInternal).problem(), true
	}
	return errCode(code).problem(), false
}

func isExists(err error) bool {
	if errors.Is(err, store.ErrSourceExists) {
		return true
	}
	_, ok := errors.AsType[controllers.ExistsError](err)
	return ok
}
