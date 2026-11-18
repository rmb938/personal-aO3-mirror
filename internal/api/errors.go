// Package api is the HTTP API served by `ao3mirror serve` (docs/part2-api.md). Handlers call
// controllers and return values; only respond writes JSON.
package api

import (
	"context"
	"errors"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Error is an API error with a fixed code (dto.ProblemCode), and optionally the parameter or
// field at fault. Handlers and helpers return it; respond turns it into a problem.
type Error struct {
	Code dto.ProblemCode
	// Field may hold a client-chosen name; the problem keeps it only when it is a
	// dto.ParamName the API declares.
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

func errCode(c dto.ProblemCode) *Error                { return &Error{Code: c} }
func errField(c dto.ProblemCode, field string) *Error { return &Error{Code: c, Field: field} }

// problem builds the problem body for e. Unknown codes become internal; unknown fields are
// dropped.
func (e *Error) problem() dto.Problem {
	code := e.Code
	info, ok := code.Info()
	if !ok {
		code = dto.CodeInternal
		info, _ = code.Info()
	}
	p := dto.Problem{Code: code, Title: info.Title, Status: info.Status}
	if ok && e.Status != 0 {
		p.Status = e.Status
	}
	if f := dto.ParamName(e.Field); f.Known() {
		p.Field = f
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
		return p, p.Code == dto.CodeInternal
	}
	var code dto.ProblemCode
	switch {
	case errors.Is(err, store.ErrNoSource), errors.Is(err, store.ErrNoSourceAdd),
		errors.Is(err, store.ErrNoJob), errors.Is(err, store.ErrNoWork),
		errors.Is(err, store.ErrNoSeries), errors.Is(err, store.ErrNoTag),
		errors.Is(err, store.ErrNoRawPage):
		code = dto.CodeNotFound
	case errors.Is(err, store.ErrStateConflict):
		code = dto.CodeConflict
	case isExists(err):
		code = dto.CodeExists
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		code = dto.CodeUnavailable
	default:
		return errCode(dto.CodeInternal).problem(), true
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
