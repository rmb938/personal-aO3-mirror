// Package dto holds the API's request and response types.
//
// Rule (docs/part2-api.md "Layout"): this package imports only the standard library. It never
// uses, imports or embeds controllers, store or model types; handlers map controller results
// into DTOs field by field. Every response type is registered with the response enforcer, so its
// json keys are exactly the keys the API may emit.
package dto

// Problem is an RFC 9457 problem details body (application/problem+json). It has only
// enumerable fields: no free-text detail.
type Problem struct {
	// Code is the machine-readable error code (a closed set).
	Code string `json:"code"`
	// Title is the fixed human-readable title for Code.
	Title string `json:"title"`
	// Status is the HTTP status code.
	Status int `json:"status"`
	// Field names the request parameter or body field at fault, when there is one. It is
	// always a name the API declares, never echoed client input.
	Field string `json:"field,omitzero"`
}

// Health is the body of GET /api/v1/healthz.
type Health struct {
	// Status is always "ok" (an enum).
	Status string `json:"status"`
}

// Version is the body of GET /api/v1/version.
type Version struct {
	// Version is the build version (an enum of the one running version).
	Version string `json:"version"`
	// ParserVersion is the parser version new parses are stamped with.
	ParserVersion int `json:"parser_version"`
}
