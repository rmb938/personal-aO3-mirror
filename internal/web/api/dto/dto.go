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
//
// Verbatim rule: a server-generated or strictly shaped non-fanwork value (a token, username,
// TOTP secret or URI, recovery code, invite URL) has a named string type with a
// `VerbatimPattern() string` method (redact.Verbatim; see verbatim.go). The enforcer passes it
// only on a full match, else the response is a 500.
package dto

import (
	"errors"
	"time"
)

// Duration is a time.Duration in JSON as a Go duration string: it marshals as
// time.Duration.String ("6h0m0s", "1.5s") and unmarshals anything time.ParseDuration accepts
// ("6h", "90m"). encoding/json/v2 has no default time.Duration format, so DTOs use this type.
// It satisfies redact.Duration structurally, so the enforcer passes its strings unhashed.
type Duration time.Duration

// Duration returns d as a time.Duration.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// MarshalText writes d as time.Duration.String.
func (d Duration) MarshalText() ([]byte, error) { return []byte(time.Duration(d).String()), nil }

// UnmarshalText parses b with time.ParseDuration.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return errors.New("dto: invalid duration") // the input is not quoted
	}
	*d = Duration(v)
	return nil
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
