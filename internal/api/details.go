package api

import (
	"slices"
	"strconv"
	"strings"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
)

// detail is a human-readable problem message from the fixed catalogue below. Every message is
// registered with the enforcer (as the only allowed values of dto.Problem.Detail and
// dto.ProblemError.Detail), so it passes unhashed. A detail can only be made by newDetail at
// package init, so it never includes client input or content: say what is wrong and how to fix
// it in terms of the API's own names and limits.
type detail struct{ msg string }

// details is the catalogue: every message newDetail made.
var details []string

// newDetail adds msg to the catalogue. Call it only in package-level var declarations.
func newDetail(msg string) detail {
	details = append(details, msg)
	return detail{msg: msg}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// oneOf is "must be one of: a, b, c" for an enum type's values.
func oneOf(e interface{ EnumValues() []string }) detail {
	return newDetail("must be one of: " + strings.Join(e.EnumValues(), ", "))
}

var (
	// Validation errors (dto.TypeValidation).
	detailInvalidParams = newDetail("one or more parameters are invalid; see errors")
	detailUnknownParam  = newDetail("this route does not take this parameter")
	detailDuplicate     = newDetail("must be given at most once")
	detailID            = newDetail("must be an integer from 1 to " + itoa(MaxID))
	detailPerPage       = newDetail("must be an integer from 1 to " + itoa(maxPerPage))
	detailPage          = newDetail("must be an integer from 1, with (page-1)*per_page at most " + itoa(maxOffset))
	detailText          = newDetail("must be valid UTF-8 text without control characters, within the length limit")
	detailRevealValue   = newDetail("must be 1")
	detailJobState      = oneOf(dto.JobState(""))
	detailJobKind       = oneOf(dto.JobKind(""))

	// Other problem types.
	detailMalformedQuery   = newDetail("the query string is malformed")
	detailTooManyParams    = newDetail("at most " + itoa(maxQueryParams) + " query parameters are allowed")
	detailInvalidBody      = newDetail("the body must be one JSON object with only the documented members, of the documented types")
	detailBodyTooLarge     = newDetail("the body must be at most " + itoa(maxBodyBytes) + " bytes")
	detailMediaType        = newDetail("Content-Type must be " + contentTypeJSON)
	detailNotJSON          = newDetail("this response is not JSON; it is served only with the " + RevealParam + " parameter")
	detailRevealDisabled   = newDetail("this server was started with --no-reveal and never returns real content")
	detailCrossOrigin      = newDetail("cross-origin requests with unsafe methods are refused")
	detailNoRoute          = newDetail("no route matches this path")
	detailNoSource         = newDetail("no source has this id")
	detailNoJob            = newDetail("no job of this kind has this id")
	detailMethodNotAllowed = newDetail("this path does not support the method; see the Allow header")
	detailStateConflict    = newDetail("the resource is not in a state that allows this")
	detailSourceExists     = newDetail("this source is already added")
	detailURITooLong       = newDetail("the path and query must be at most " + itoa(maxURLBytes) + " bytes")
	detailRateLimited      = newDetail("too many requests; retry after the Retry-After delay")
	detailBusy             = newDetail("the server is busy; retry after the Retry-After delay")
	detailTimeout          = newDetail("the request did not finish in time; retry later")
)

// detailCatalogue returns every message, sorted and deduplicated.
func detailCatalogue() []string {
	out := slices.Clone(details)
	slices.Sort(out)
	return slices.Compact(out)
}
