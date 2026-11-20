package problem

import (
	"strings"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// Error is an API error: a problem type, an optional detail and, for a validation error, the
// invalid parts. Handlers and helpers return it; the api package turns it into a problem.
type Error struct {
	Type dto.ProblemType
	// Status overrides the type's default status when non-zero (e.g. a reveal-required that
	// replaces a non-JSON response is a 403).
	Status int
	Detail Detail
	Errors []Item
}

// Item is one invalid part of a request: a parameter (Param) or a body member (Pointer).
type Item struct {
	// Param may hold a client-chosen name; the problem keeps it only when it is a
	// dto.ParamName the API declares.
	Param string
	// Pointer is a JSON Pointer into the body, built from the request DTO's json keys
	// (Validation.Member).
	Pointer string
	Detail  Detail
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("api: ")
	b.WriteString(e.Type.Slug())
	for _, pe := range e.Errors {
		if dto.ParamName(pe.Param).Known() {
			b.WriteString(" (")
			b.WriteString(pe.Param)
			b.WriteByte(')')
		}
	}
	return b.String()
}

// New is a problem of type t with no detail.
func New(t dto.ProblemType) *Error { return &Error{Type: t} }

// WithDetail is a problem of type t with detail d.
func WithDetail(t dto.ProblemType, d Detail) *Error { return &Error{Type: t, Detail: d} }

// ForParam is a validation error for one parameter (middleware; handlers collect with a
// Validation).
func ForParam(param string, d Detail) *Error {
	return &Error{Type: dto.TypeValidation, Detail: DetailInvalidRequest, Errors: []Item{{Param: param, Detail: d}}}
}

// Problem builds the problem body for e. Unknown types become internal; undeclared parameter
// names are dropped.
func (e *Error) Problem() dto.Problem {
	t := e.Type
	info, ok := t.Info()
	if !ok {
		return New(dto.TypeInternal).Problem()
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
