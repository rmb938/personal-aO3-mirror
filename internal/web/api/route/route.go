// Package route holds the API's route patterns and builds paths from them. The api package
// registers these patterns with its router and builds Location headers with Path; the API
// client (internal/apiclient) builds its request paths the same way, so the two cannot drift.
//
// It imports only the standard library.
package route

import (
	"strconv"
	"strings"
)

// The route patterns the router registers ({param} segments are numeric IDs but in TagByName).
const (
	Healthz   = "/api/v1/healthz"
	Version   = "/api/v1/version"
	Sources   = "/api/v1/sources"
	Source    = "/api/v1/sources/{id}"
	SourceAdd = "/api/v1/source-adds/{job_id}"
	Jobs      = "/api/v1/jobs"
	Job       = "/api/v1/jobs/{id}"
	Status    = "/api/v1/status"
	Works     = "/api/v1/works"
	Work      = "/api/v1/works/{id}"
	WorkSkin  = "/api/v1/works/{id}/skin"
	Chapters  = "/api/v1/works/{id}/chapters"
	Chapter   = "/api/v1/works/{id}/chapters/{position}"
	Tag       = "/api/v1/tags/{id}"
	TagWorks  = "/api/v1/tags/{id}/works"
	// TagByName's {name} is a free-text tag name (AO3's /tags/ path form), not an id, so Path
	// cannot build it.
	TagByName = "/api/v1/tags/by-name/{name}"

	// Admin socket only.
	SourceConfirm = "/api/v1/sources/{id}/confirm"
	SourcePause   = "/api/v1/sources/{id}/pause"
	SourceResume  = "/api/v1/sources/{id}/resume"
	SourceCancel  = "/api/v1/sources/{id}/cancel"
	SourceRefresh = "/api/v1/sources/{id}/refresh"
	DebugJob      = "/api/v1/debug/jobs/{id}"
	DebugWork     = "/api/v1/debug/works/{id}"
)

// Path fills pattern's {param} segments with ids, in order: Path(Source, 3) is
// /api/v1/sources/3. It builds paths from IDs only, never from input or content. A count
// mismatch is a programming error and panics (the API's recoverer turns it into a 500).
func Path(pattern string, ids ...int64) string {
	segs := strings.Split(pattern, "/")
	n := 0
	for i, s := range segs {
		if !strings.HasPrefix(s, "{") {
			continue
		}
		if n == len(ids) {
			panic("route: Path " + pattern + ": too few ids")
		}
		segs[i] = strconv.FormatInt(ids[n], 10)
		n++
	}
	if n != len(ids) {
		panic("route: Path " + pattern + ": too many ids")
	}
	return strings.Join(segs, "/")
}
