package dto

import (
	"net/http"
	"slices"
)

// ProblemCode is a problem's machine-readable error kind (an enum).
type ProblemCode string

const (
	CodeBadRequest           ProblemCode = "bad_request"
	CodeInvalidParam         ProblemCode = "invalid_param"
	CodeUnknownParam         ProblemCode = "unknown_param"
	CodeDuplicateParam       ProblemCode = "duplicate_param"
	CodeInvalidBody          ProblemCode = "invalid_body"
	CodeBodyTooLarge         ProblemCode = "body_too_large"
	CodeUnsupportedMediaType ProblemCode = "unsupported_media_type"
	CodeRevealRequired       ProblemCode = "reveal_required"
	CodeRevealDisabled       ProblemCode = "reveal_disabled"
	CodeForbidden            ProblemCode = "forbidden"
	CodeNotFound             ProblemCode = "not_found"
	CodeMethodNotAllowed     ProblemCode = "method_not_allowed"
	CodeConflict             ProblemCode = "conflict"
	CodeExists               ProblemCode = "exists"
	CodeURITooLong           ProblemCode = "uri_too_long"
	CodeRateLimited          ProblemCode = "rate_limited"
	CodeUnavailable          ProblemCode = "unavailable"
	CodeInternal             ProblemCode = "internal"
)

// ProblemTitle is the fixed human-readable title of a ProblemCode (an enum).
type ProblemTitle string

// ProblemInfo is a code's default HTTP status and its title.
type ProblemInfo struct {
	Status int
	Title  ProblemTitle
}

// problemInfo is the one table of codes: every ProblemCode and ProblemTitle value comes from
// it.
var problemInfo = map[ProblemCode]ProblemInfo{
	CodeBadRequest:           {http.StatusBadRequest, "Bad request"},
	CodeInvalidParam:         {http.StatusBadRequest, "Invalid parameter"},
	CodeUnknownParam:         {http.StatusBadRequest, "Unknown parameter"},
	CodeDuplicateParam:       {http.StatusBadRequest, "Duplicate parameter"},
	CodeInvalidBody:          {http.StatusBadRequest, "Invalid request body"},
	CodeBodyTooLarge:         {http.StatusRequestEntityTooLarge, "Request body too large"},
	CodeUnsupportedMediaType: {http.StatusUnsupportedMediaType, "Unsupported media type"},
	CodeRevealRequired:       {http.StatusBadRequest, "Reveal required"},
	CodeRevealDisabled:       {http.StatusForbidden, "Reveal disabled on this server"},
	CodeForbidden:            {http.StatusForbidden, "Forbidden"},
	CodeNotFound:             {http.StatusNotFound, "Not found"},
	CodeMethodNotAllowed:     {http.StatusMethodNotAllowed, "Method not allowed"},
	CodeConflict:             {http.StatusConflict, "State conflict"},
	CodeExists:               {http.StatusConflict, "Already exists"},
	CodeURITooLong:           {http.StatusRequestURITooLong, "URI too long"},
	CodeRateLimited:          {http.StatusTooManyRequests, "Too many requests"},
	CodeUnavailable:          {http.StatusServiceUnavailable, "Service unavailable"},
	CodeInternal:             {http.StatusInternalServerError, "Internal server error"},
}

// Info returns c's status and title, and whether c is a known code.
func (c ProblemCode) Info() (ProblemInfo, bool) {
	info, ok := problemInfo[c]
	return info, ok
}

// EnumValues lists every code.
func (ProblemCode) EnumValues() []string {
	vals := make([]string, 0, len(problemInfo))
	for c := range problemInfo {
		vals = append(vals, string(c))
	}
	slices.Sort(vals)
	return vals
}

// EnumValues lists every code's title.
func (ProblemTitle) EnumValues() []string {
	vals := make([]string, 0, len(problemInfo))
	for _, info := range problemInfo {
		vals = append(vals, string(info.Title))
	}
	slices.Sort(vals)
	return vals
}

// ParamName is a request parameter or body field name the API declares (an enum). A problem's
// field and the access log's param names are only ever one of these, never client input. Add
// names as routes add parameters.
type ParamName string

const (
	ParamPage     ParamName = "page"
	ParamPerPage  ParamName = "per_page"
	ParamID       ParamName = "id"
	ParamJobID    ParamName = "job_id"
	ParamState    ParamName = "state"
	ParamKind     ParamName = "kind"
	ParamSourceID ParamName = "source_id"
	// ParamReveal is the query parameter that asks for real fanwork content on one request
	// (api.RevealParam).
	ParamReveal ParamName = "reveal_fanwork"
)

var paramNames = []ParamName{ParamPage, ParamPerPage, ParamID, ParamJobID, ParamState, ParamKind, ParamSourceID, ParamReveal}

// Known reports whether p is a declared name.
func (p ParamName) Known() bool { return slices.Contains(paramNames, p) }

// EnumValues lists every declared name.
func (ParamName) EnumValues() []string {
	vals := make([]string, len(paramNames))
	for i, p := range paramNames {
		vals[i] = string(p)
	}
	return vals
}
