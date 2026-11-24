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

	// Auth entry points (stage 16f): no access token needed (Anonymous).
	AuthSignup        = "/api/v1/auth/signup"
	AuthSignupConfirm = "/api/v1/auth/signup/confirm"
	AuthReset         = "/api/v1/auth/reset"
	AuthResetConfirm  = "/api/v1/auth/reset/confirm"
	AuthLogin         = "/api/v1/auth/login"
	AuthLoginTOTP     = "/api/v1/auth/login/totp"
	AuthRefresh       = "/api/v1/auth/refresh"

	// The signed-in user's own account (stage 16f): like every route but the ones above, they
	// need a bearer access token, and on the admin socket (which has no user) they are always 401.
	AuthLogout           = "/api/v1/auth/logout"
	Account              = "/api/v1/account"
	AccountPassword      = "/api/v1/account/password" //nolint:gosec // G101: a route pattern, not a credential
	AccountRecoveryCodes = "/api/v1/account/recovery-codes"
	AccountSessions      = "/api/v1/account/sessions"
	AccountSession       = "/api/v1/account/sessions/{id}"

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
	AdminMasterKeyRotate  = "/api/v1/admin/master-keys/rotate"
)

// Anonymous reports whether the public listener serves pattern without an access token: the
// health check and the auth entry points (stage 16g). Every other route, and a path that
// matches no route, needs one. The admin socket never asks for one.
func Anonymous(pattern string) bool {
	switch pattern {
	case Healthz, AuthSignup, AuthSignupConfirm, AuthReset, AuthResetConfirm, AuthLogin, AuthLoginTOTP, AuthRefresh:
		return true
	}
	return false
}

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
