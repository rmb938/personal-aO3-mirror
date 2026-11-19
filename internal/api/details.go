package api

import (
	"strconv"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/api/problem"
)

// The api package's part of the problem detail catalogue (problem.NewDetail: package-level
// vars only, never built from input or content). Details shared with request types
// (problem.DetailInvalidRequest, problem.DetailText, problem.URLDetail) live in problem.

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

var (
	// Validation errors (dto.TypeValidation).
	detailUnknownParam = problem.NewDetail("this route does not take this parameter")
	detailDuplicate    = problem.NewDetail("must be given at most once")
	detailID           = problem.NewDetail("must be an integer from 1 to " + itoa(MaxID))
	detailPerPage      = problem.NewDetail("must be an integer from 1 to " + itoa(maxPerPage))
	detailPage         = problem.NewDetail("must be an integer from 1, with (page-1)*per_page at most " + itoa(maxOffset))
	detailRevealValue  = problem.NewDetail("must be 1")
	detailJobState     = problem.OneOf(dto.JobState(""))
	detailJobKind      = problem.OneOf(dto.JobKind(""))

	// Other problem types.
	detailMalformedQuery   = problem.NewDetail("the query string is malformed")
	detailTooManyParams    = problem.NewDetail("at most " + itoa(maxQueryParams) + " query parameters are allowed")
	detailInvalidBody      = problem.NewDetail("the body must be one JSON object with only the documented members, of the documented types")
	detailBodyTooLarge     = problem.NewDetail("the body must be at most " + itoa(maxBodyBytes) + " bytes")
	detailMediaType        = problem.NewDetail("Content-Type must be " + contentTypeJSON)
	detailNotJSON          = problem.NewDetail("this response is not JSON; it is served only with the " + RevealParam + " parameter")
	detailRevealDisabled   = problem.NewDetail("this server was started with --no-reveal and never returns real content")
	detailCrossOrigin      = problem.NewDetail("cross-origin requests with unsafe methods are refused")
	detailNoRoute          = problem.NewDetail("no route matches this path")
	detailNoSource         = problem.NewDetail("no source has this id")
	detailNoJob            = problem.NewDetail("no job of this kind has this id")
	detailMethodNotAllowed = problem.NewDetail("this path does not support the method; see the Allow header")
	detailStateConflict    = problem.NewDetail("the resource is not in a state that allows this")
	detailSourceExists     = problem.NewDetail("this source is already added")
	detailURITooLong       = problem.NewDetail("the path and query must be at most " + itoa(maxURLBytes) + " bytes")
	detailRateLimited      = problem.NewDetail("too many requests; retry after the Retry-After delay")
	detailBusy             = problem.NewDetail("the server is busy; retry after the Retry-After delay")
	detailTimeout          = problem.NewDetail("the request did not finish in time; retry later")
)
