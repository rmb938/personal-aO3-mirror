// Package dto holds the API's request and response types.
//
// Rule (docs/part2-api.md "Layout"): this package imports only the standard library. It never
// uses, imports or embeds controllers, store or model types; handlers map controller results
// into DTOs field by field. Every response type is registered with the response enforcer, so its
// json keys are exactly the keys the API may emit.
//
// Enum rule: a string field with a closed value set has a named string type here with an
// `EnumValues() []string` method (it satisfies redact.Enum structurally; this package never
// imports redact). The enforcer passes those values unhashed. A plain string field is free
// text and is always hashed.
package dto

// Problem is an RFC 9457 problem details body (application/problem+json). It has only
// enumerable fields: no free-text detail.
type Problem struct {
	// Code is the machine-readable error code.
	Code ProblemCode `json:"code"`
	// Title is the fixed human-readable title for Code.
	Title ProblemTitle `json:"title"`
	// Status is the HTTP status code.
	Status int `json:"status"`
	// Field names the request parameter or body field at fault, when there is one. It is
	// always a name the API declares, never echoed client input.
	Field ParamName `json:"field,omitzero"`
}

// HealthStatus is Health.Status (an enum).
type HealthStatus string

// HealthOK is the only HealthStatus.
const HealthOK HealthStatus = "ok"

// EnumValues lists every HealthStatus.
func (HealthStatus) EnumValues() []string { return []string{string(HealthOK)} }

// Health is the body of GET /api/v1/healthz.
type Health struct {
	// Status is always "ok".
	Status HealthStatus `json:"status"`
}

// Version is the body of GET /api/v1/version.
type Version struct {
	// Version is the build version (an enum of the one running version).
	Version string `json:"version"`
	// ParserVersion is the parser version new parses are stamped with.
	ParserVersion int `json:"parser_version"`
}
