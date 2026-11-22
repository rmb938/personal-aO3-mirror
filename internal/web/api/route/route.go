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
	Tags      = "/api/v1/tags"
	Tag       = "/api/v1/tags/{id}"
	TagWorks  = "/api/v1/tags/{id}/works"
	// TagByName's {name} is a free-text tag name (AO3's /tags/ path form), not an id, so Path
	// cannot build it.
	TagByName       = "/api/v1/tags/by-name/{name}"
	Media           = "/api/v1/media"
	MediumFandoms   = "/api/v1/media/{id}/fandoms"
	Series          = "/api/v1/series/{id}"
	SeriesWorks     = "/api/v1/series/{id}/works"
	Creators        = "/api/v1/creators"
	Creator         = "/api/v1/creators/{id}"
	CreatorWorks    = "/api/v1/creators/{id}/works"
	Collection      = "/api/v1/collections/{id}"
	CollectionWorks = "/api/v1/collections/{id}/works"
	// Image's {sha256} is a downloaded image's hash (a work's images[].sha256), not an id, so
	// Path cannot build it.
	Image = "/api/v1/images/{sha256}"

	// Auth entry points (stage 16f): no access token needed.
	AuthSignup        = "/api/v1/auth/signup"
	AuthSignupConfirm = "/api/v1/auth/signup/confirm"
	AuthReset         = "/api/v1/auth/reset"
	AuthResetConfirm  = "/api/v1/auth/reset/confirm"

	// Admin socket only.
	SourceConfirm = "/api/v1/sources/{id}/confirm"
	SourcePause   = "/api/v1/sources/{id}/pause"
	SourceResume  = "/api/v1/sources/{id}/resume"
	SourceCancel  = "/api/v1/sources/{id}/cancel"
	SourceRefresh = "/api/v1/sources/{id}/refresh"
	DebugJob      = "/api/v1/debug/jobs/{id}"
	DebugWork     = "/api/v1/debug/works/{id}"

	// Admin socket only: accounts (stage 16).
	AdminInvites          = "/api/v1/admin/invites"
	AdminInvite           = "/api/v1/admin/invites/{id}"
	AdminUsers            = "/api/v1/admin/users"
	AdminUser             = "/api/v1/admin/users/{id}"
	AdminUserDisable      = "/api/v1/admin/users/{id}/disable"
	AdminUserEnable       = "/api/v1/admin/users/{id}/enable"
	AdminUserReset        = "/api/v1/admin/users/{id}/reset"
	AdminUserSessions     = "/api/v1/admin/users/{id}/sessions"
	AdminSession          = "/api/v1/admin/sessions/{id}"
	AdminSigningKeyRotate = "/api/v1/admin/signing-keys/rotate"
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
