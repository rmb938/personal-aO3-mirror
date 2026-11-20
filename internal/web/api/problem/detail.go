// Package problem builds the API's RFC 9457 problems (docs/api/problems.md): the fixed detail
// catalogue, the Error handlers return, and the Validation collector that gathers every
// invalid part of a request. It imports only dto, ao3url and the standard library, so both
// the api package and request types (stage 4) can use it.
package problem

import (
	"errors"
	"slices"
	"strings"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
)

// Detail is a human-readable problem message from the fixed catalogue. Every message is
// registered with the enforcer (as the only allowed values of dto.Problem.Detail and
// dto.ProblemError.Detail), so it passes unhashed. A Detail can only be made by NewDetail at
// package init, so it never includes client input or content: say what is wrong and how to fix
// it in terms of the API's own names and limits.
type Detail struct{ msg string }

// Message is the detail's text ("" for the zero Detail, which a problem omits).
func (d Detail) Message() string { return d.msg }

// details is the catalogue: every message NewDetail made, in any package.
var details []string

// NewDetail adds msg to the catalogue. Call it only in package-level var declarations.
func NewDetail(msg string) Detail {
	details = append(details, msg)
	return Detail{msg: msg}
}

// OneOf is "must be one of: a, b, c" for an enum type's values. Like NewDetail, call it only
// in package-level var declarations.
func OneOf(e interface{ EnumValues() []string }) Detail {
	return NewDetail("must be one of: " + strings.Join(e.EnumValues(), ", "))
}

// Catalogue returns every message, sorted and deduplicated.
func Catalogue() []string {
	out := slices.Clone(details)
	slices.Sort(out)
	return slices.Compact(out)
}

var (
	// DetailInvalidRequest is a validation-error's own detail; its errors list the parts.
	DetailInvalidRequest = NewDetail("one or more parts of the request are invalid; see errors")
	// DetailText is for a string that fails ValidText.
	DetailText = NewDetail("must be valid UTF-8 text without control characters, within the length limit")
)

// urlDetails explains each reason ao3url.Parse rejects a source URL, with ao3url's fixed hint
// (it never holds the URL).
var urlDetails = func() map[ao3url.Reason]Detail {
	m := make(map[ao3url.Reason]Detail, len(ao3url.Reasons()))
	for _, r := range ao3url.Reasons() {
		m[r] = NewDetail(r.Hint())
	}
	return m
}()

// URLDetail is the detail for a rejected source URL (err from ao3url.Parse), and whether err
// is one.
func URLDetail(err error) (Detail, bool) {
	ue, ok := errors.AsType[*ao3url.URLError](err)
	if !ok {
		return Detail{}, false
	}
	d, ok := urlDetails[ue.Reason]
	return d, ok
}
