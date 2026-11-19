// Package api is the HTTP API served by `ao3mirror serve` (docs/part2-api.md). Handlers call
// controllers and return values; only respond writes JSON.
package api

import (
	"context"
	"errors"
	"strings"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Error is an API error: a problem type, an optional detail and, for a validation error, the
// invalid parameters. Handlers and helpers return it; respond turns it into a problem.
type Error struct {
	Type dto.ProblemType
	// Status overrides the type's default status when non-zero (e.g. a reveal-required that
	// replaces a non-JSON response is a 403).
	Status int
	Detail detail
	Errors []ErrorItem
}

// ErrorItem is one invalid part of a request: a parameter (Param) or a body member (Pointer).
type ErrorItem struct {
	// Param may hold a client-chosen name; the problem keeps it only when it is a
	// dto.ParamName the API declares.
	Param string
	// Pointer is a JSON Pointer into the body, built from the request DTO's json keys
	// (validation.member).
	Pointer string
	Detail  detail
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("api: " + e.Type.Slug())
	for _, pe := range e.Errors {
		if dto.ParamName(pe.Param).Known() {
			b.WriteString(" (" + pe.Param + ")")
		}
	}
	return b.String()
}

// errType is a problem of type t with no detail.
func errType(t dto.ProblemType) *Error { return &Error{Type: t} }

// errDetail is a problem of type t with detail d.
func errDetail(t dto.ProblemType, d detail) *Error { return &Error{Type: t, Detail: d} }

// errParam is a validation error for one parameter (middleware; handlers collect with params).
func errParam(param string, d detail) *Error {
	return &Error{Type: dto.TypeValidation, Detail: detailInvalidRequest, Errors: []ErrorItem{{Param: param, Detail: d}}}
}

// problem builds the problem body for e. Unknown types become internal; undeclared parameter
// names are dropped.
func (e *Error) problem() dto.Problem {
	t := e.Type
	info, ok := t.Info()
	if !ok {
		return errType(dto.TypeInternal).problem()
	}
	p := dto.Problem{Type: t, Title: info.Title, Status: info.Status, Detail: e.Detail.msg}
	if e.Status != 0 {
		p.Status = e.Status
	}
	for _, pe := range e.Errors {
		item := dto.ProblemError{Detail: pe.Detail.msg, Pointer: pe.Pointer}
		if f := dto.ParamName(pe.Param); f.Known() {
			item.Parameter = f
		}
		p.Errors = append(p.Errors, item)
	}
	return p
}

// problemFor maps any handler error to a problem, in one place: *Error as is; store
// not-found sentinels → 404; state conflicts → 409; an existing source → 409 already-exists; a
// cancelled or timed-out request → 503; anything else → 500 internal. internal reports
// whether the error is unexpected (the caller logs it).
func problemFor(err error) (p dto.Problem, internal bool) {
	if e, ok := errors.AsType[*Error](err); ok {
		p = e.problem()
		return p, p.Type == dto.TypeInternal
	}
	var e *Error
	switch {
	case errors.Is(err, store.ErrNoSource):
		e = errDetail(dto.TypeNotFound, detailNoSource)
	case errors.Is(err, store.ErrNoJob), errors.Is(err, store.ErrNoSourceAdd):
		e = errDetail(dto.TypeNotFound, detailNoJob)
	case errors.Is(err, store.ErrNoWork), errors.Is(err, store.ErrNoSeries), errors.Is(err, store.ErrNoTag),
		errors.Is(err, store.ErrNoRawPage):
		e = errType(dto.TypeNotFound)
	case errors.Is(err, store.ErrStateConflict):
		e = errDetail(dto.TypeStateConflict, detailStateConflict)
	case isExists(err):
		e = errDetail(dto.TypeAlreadyExists, detailSourceExists)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		e = errDetail(dto.TypeUnavailable, detailTimeout)
	default:
		return errType(dto.TypeInternal).problem(), true
	}
	return e.problem(), false
}

func isExists(err error) bool {
	if errors.Is(err, store.ErrSourceExists) {
		return true
	}
	_, ok := errors.AsType[controllers.ExistsError](err)
	return ok
}
