package api

import (
	"strconv"
	"strings"
)

// The route patterns the router registers. Location headers are built from them with pathFor,
// so a Location always names a route that exists.
const (
	routeHealthz   = "/api/v1/healthz"
	routeVersion   = "/api/v1/version"
	routeSources   = "/api/v1/sources"
	routeSource    = "/api/v1/sources/{id}"
	routeSourceAdd = "/api/v1/source-adds/{job_id}"
	routeJobs      = "/api/v1/jobs"
	routeJob       = "/api/v1/jobs/{id}"
	routeStatus    = "/api/v1/status"

	// Admin socket only.
	routeSourceConfirm = "/api/v1/sources/{id}/confirm"
	routeSourcePause   = "/api/v1/sources/{id}/pause"
	routeSourceResume  = "/api/v1/sources/{id}/resume"
	routeSourceCancel  = "/api/v1/sources/{id}/cancel"
)

// pathFor fills route's {param} segments with ids, in order: pathFor(routeSource, 3) is
// /api/v1/sources/3. It builds paths from IDs only, never from input or content. A count
// mismatch is a programming error and panics (the recoverer turns it into a 500).
func pathFor(route string, ids ...int64) string {
	segs := strings.Split(route, "/")
	n := 0
	for i, s := range segs {
		if !strings.HasPrefix(s, "{") {
			continue
		}
		if n == len(ids) {
			panic("api: pathFor " + route + ": too few ids")
		}
		segs[i] = strconv.FormatInt(ids[n], 10)
		n++
	}
	if n != len(ids) {
		panic("api: pathFor " + route + ": too many ids")
	}
	return strings.Join(segs, "/")
}
