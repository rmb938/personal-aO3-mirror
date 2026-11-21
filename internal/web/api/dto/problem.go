package dto

import (
	"net/http"
	"slices"
	"strings"
)

// Problem is an RFC 9457 problem details object (application/problem+json).
//
// Every string in it comes from a closed set, so it passes the enforcer unhashed and can never
// carry fanwork content or echo client input:
//   - type and title are fixed per problem type (ProblemType, ProblemTitle);
//   - detail and each errors[].detail are messages from the api package's fixed catalogue,
//     registered with the enforcer at startup (they never include client input);
//   - errors[].parameter is a ParamName;
//   - errors[].pointer must be a JSON Pointer to a member of a registered request type.
type Problem struct {
	// Type identifies the problem type: an absolute URI of its section in docs/api/problems.md.
	Type ProblemType `json:"type"`
	// Title is the type's fixed summary.
	Title ProblemTitle `json:"title"`
	// Status is the HTTP status code of the response.
	Status int `json:"status"`
	// Detail explains this occurrence (a catalogue message).
	Detail string `json:"detail,omitzero"`
	// Errors lists each invalid parameter of a validation error (an extension member).
	Errors []ProblemError `json:"errors,omitzero"`
}

// ProblemError is one invalid part of a request.
type ProblemError struct {
	// Detail says what is wrong and how to fix it (a catalogue message).
	Detail string `json:"detail"`
	// Parameter names the query or path parameter at fault (omitted when it is not a declared
	// name, e.g. an unknown parameter).
	Parameter ParamName `json:"parameter,omitzero"`
	// Pointer is the RFC 6901 JSON Pointer of the body member at fault (e.g. "/url"). The
	// enforcer passes it only when it names a member of a registered request type.
	Pointer string `json:"pointer,omitzero"`
}

// ProblemType is a problem type URI (an enum).
type ProblemType string

// ProblemTypeBase is the prefix of every ProblemType: docs/api/problems.md on GitHub, so each
// type resolves to its documentation (RFC 9457 section 4).
const ProblemTypeBase = "https://github.com/rmb938/personal-aO3-mirror/blob/main/docs/api/problems.md#"

const (
	TypeBadRequest           ProblemType = ProblemTypeBase + "bad-request"
	TypeValidation           ProblemType = ProblemTypeBase + "validation-error"
	TypeInvalidBody          ProblemType = ProblemTypeBase + "invalid-body"
	TypeBodyTooLarge         ProblemType = ProblemTypeBase + "body-too-large"
	TypeUnsupportedMediaType ProblemType = ProblemTypeBase + "unsupported-media-type"
	TypeRevealRequired       ProblemType = ProblemTypeBase + "reveal-required"
	TypeRevealDisabled       ProblemType = ProblemTypeBase + "reveal-disabled"
	TypeForbidden            ProblemType = ProblemTypeBase + "forbidden"
	TypeNotFound             ProblemType = ProblemTypeBase + "not-found"
	TypeMethodNotAllowed     ProblemType = ProblemTypeBase + "method-not-allowed"
	TypeStateConflict        ProblemType = ProblemTypeBase + "state-conflict"
	TypeAlreadyExists        ProblemType = ProblemTypeBase + "already-exists"
	TypeWorkTooLarge         ProblemType = ProblemTypeBase + "work-too-large"
	TypeURITooLong           ProblemType = ProblemTypeBase + "uri-too-long"
	TypeRateLimited          ProblemType = ProblemTypeBase + "rate-limited"
	TypeUnavailable          ProblemType = ProblemTypeBase + "unavailable"
	TypeInternal             ProblemType = ProblemTypeBase + "internal-error"
)

// Slug is the type's section anchor in docs/api/problems.md (e.g. "not-found").
func (t ProblemType) Slug() string { return strings.TrimPrefix(string(t), ProblemTypeBase) }

// ProblemTitle is the fixed human-readable title of a ProblemType (an enum).
type ProblemTitle string

// ProblemInfo is a type's default HTTP status and its title.
type ProblemInfo struct {
	Status int
	Title  ProblemTitle
}

// problemInfo is the one table of problem types: every ProblemType and ProblemTitle value
// comes from it. docs/api/problems.md documents each one (a test checks they match).
var problemInfo = map[ProblemType]ProblemInfo{
	TypeBadRequest:           {http.StatusBadRequest, "Bad Request"},
	TypeValidation:           {http.StatusUnprocessableEntity, "Validation Error"},
	TypeInvalidBody:          {http.StatusBadRequest, "Invalid Request Body"},
	TypeBodyTooLarge:         {http.StatusRequestEntityTooLarge, "Content Too Large"},
	TypeUnsupportedMediaType: {http.StatusUnsupportedMediaType, "Unsupported Media Type"},
	TypeRevealRequired:       {http.StatusBadRequest, "Reveal Required"},
	TypeRevealDisabled:       {http.StatusForbidden, "Reveal Disabled"},
	TypeForbidden:            {http.StatusForbidden, "Forbidden"},
	TypeNotFound:             {http.StatusNotFound, "Not Found"},
	TypeMethodNotAllowed:     {http.StatusMethodNotAllowed, "Method Not Allowed"},
	TypeStateConflict:        {http.StatusConflict, "State Conflict"},
	TypeAlreadyExists:        {http.StatusConflict, "Already Exists"},
	TypeWorkTooLarge:         {http.StatusUnprocessableEntity, "Work Too Large"},
	TypeURITooLong:           {http.StatusRequestURITooLong, "URI Too Long"},
	TypeRateLimited:          {http.StatusTooManyRequests, "Too Many Requests"},
	TypeUnavailable:          {http.StatusServiceUnavailable, "Service Unavailable"},
	TypeInternal:             {http.StatusInternalServerError, "Internal Server Error"},
}

// Info returns t's default status and title, and whether t is a known type.
func (t ProblemType) Info() (ProblemInfo, bool) {
	info, ok := problemInfo[t]
	return info, ok
}

// EnumValues lists every type.
func (ProblemType) EnumValues() []string {
	vals := make([]string, 0, len(problemInfo))
	for t := range problemInfo {
		vals = append(vals, string(t))
	}
	slices.Sort(vals)
	return vals
}

// EnumValues lists every type's title.
func (ProblemTitle) EnumValues() []string {
	vals := make([]string, 0, len(problemInfo))
	for _, info := range problemInfo {
		vals = append(vals, string(info.Title))
	}
	slices.Sort(vals)
	return vals
}

// ParamName is a query or path parameter name the API declares (an enum). A problem's
// errors[].parameter and the access log's param names are only ever one of these, never client
// input. Add names as routes add parameters; body members are not parameters.
type ParamName string

const (
	ParamPage     ParamName = "page"
	ParamPerPage  ParamName = "per_page"
	ParamID       ParamName = "id"
	ParamJobID    ParamName = "job_id"
	ParamState    ParamName = "state"
	ParamKind     ParamName = "kind"
	ParamSourceID ParamName = "source_id"
	ParamPosition ParamName = "position"
	// ParamReveal is the query parameter that asks for real fanwork content on one request
	// (api.RevealParam).
	ParamReveal ParamName = "reveal_fanwork"
)

var paramNames = []ParamName{ParamPage, ParamPerPage, ParamID, ParamJobID, ParamState, ParamKind, ParamSourceID, ParamPosition,
	ParamReveal}

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
