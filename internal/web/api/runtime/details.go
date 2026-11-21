package runtime

import (
	"strconv"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
)

// The api package's part of the problem detail catalogue (problem.NewDetail: package-level
// vars only, never built from input or content). Details shared with request types
// (problem.DetailInvalidRequest, problem.DetailText, problem.URLDetail) live in problem.

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// workCapMiB is controllers.MaxWorkChaptersBytes in MiB.
var workCapMiB = itoa(controllers.MaxWorkChaptersBytes >> 20)

var (
	// Validation errors (dto.TypeValidation).
	DetailUnknownParam = problem.NewDetail("this route does not take this parameter")
	DetailDuplicate    = problem.NewDetail("must be given at most once")
	DetailID           = problem.NewDetail("must be an integer from 1 to " + itoa(MaxID))
	DetailPosition     = problem.NewDetail("must be an integer from 1 to " + itoa(MaxPosition))
	DetailTagName      = problem.NewDetail("must be a tag name in AO3's /tags/ path form, of 1 to " + itoa(MaxTagNameRunes) +
		" characters, with no control characters")
	DetailSHA256      = problem.NewDetail("must be a SHA-256 in hex: 64 characters, 0-9 and a-f")
	DetailPerPage     = problem.NewDetail("must be an integer from 1 to " + itoa(MaxPerPage))
	DetailPage        = problem.NewDetail("must be an integer from 1, with (page-1)*per_page at most " + itoa(MaxOffset))
	DetailRevealValue = problem.NewDetail("must be 1")
	DetailJobState    = problem.OneOf(dto.JobState(""))
	DetailJobKind     = problem.OneOf(dto.JobKind(""))
	DetailWorkSort    = problem.OneOf(dto.WorkSort(""))
	DetailDirection   = problem.OneOf(dto.SortDirection(""))
	DetailTagSort     = problem.OneOf(dto.TagSort(""))

	// Source add body (POST /api/v1/sources); the body's own details are in request.
	DetailConfirmAdminOnly = problem.NewDetail("only the admin socket accepts confirm; a large source added here waits for `source confirm`")

	// Other problem types.
	DetailMalformedQuery   = problem.NewDetail("the query string is malformed")
	DetailTooManyParams    = problem.NewDetail("at most " + itoa(MaxQueryParams) + " query parameters are allowed")
	DetailInvalidBody      = problem.NewDetail("the body must be one JSON object with only the documented members, of the documented types")
	DetailBodyTooLarge     = problem.NewDetail("the body must be at most " + itoa(MaxBodyBytes) + " bytes")
	DetailMediaType        = problem.NewDetail("Content-Type must be " + ContentTypeJSON)
	DetailNotJSON          = problem.NewDetail("this response is not JSON; it is served only with the " + RevealParam + " parameter")
	DetailFreeText         = problem.NewDetail("a free-text member matches hidden text, so it needs the " + RevealParam + " parameter")
	DetailFreeTextPath     = problem.NewDetail("a name in the path matches hidden text, so it needs the " + RevealParam + " parameter")
	DetailRevealDisabled   = problem.NewDetail("this server was started with --no-reveal and never returns real content")
	DetailCrossOrigin      = problem.NewDetail("cross-origin requests with unsafe methods are refused")
	DetailNoRoute          = problem.NewDetail("no route matches this path")
	DetailNoSource         = problem.NewDetail("no source has this id")
	DetailNoJob            = problem.NewDetail("no job of this kind has this id")
	DetailNoWork           = problem.NewDetail("no work has this id")
	DetailNoChapter        = problem.NewDetail("the work has no stored chapter at this position")
	DetailNoTag            = problem.NewDetail("no stored tag has this id or name")
	DetailNoMedium         = problem.NewDetail("no stored media tag has this id")
	DetailNoSeries         = problem.NewDetail("no stored series has this id")
	DetailNoCreator        = problem.NewDetail("no stored creator has this id")
	DetailNoCollection     = problem.NewDetail("no stored collection has this id")
	DetailNoImage          = problem.NewDetail("no downloaded image has this hash")
	DetailWorkTooLarge     = problem.NewDetail("the work's chapters are over " + workCapMiB + " MiB together; read them one at a time from " + route.Chapter)
	DetailMethodNotAllowed = problem.NewDetail("this path does not support the method; see the Allow header")
	DetailStateConflict    = problem.NewDetail("the resource is not in a state that allows this")
	DetailSourceExists     = problem.NewDetail("this source is already added")
	DetailURITooLong       = problem.NewDetail("the path and query must be at most " + itoa(MaxURLBytes) + " bytes")
	DetailRateLimited      = problem.NewDetail("too many requests; retry after the Retry-After delay")
	DetailBusy             = problem.NewDetail("the server is busy; retry after the Retry-After delay")
	DetailTimeout          = problem.NewDetail("the request did not finish in time; retry later")
)
