package integration

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

// The admin socket's account routes (stage 16e) over the seeded accounts (seed_auth_test.go).

func decodeJSON[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
	return v
}

func keys(m map[string]string) []string { return slices.Sorted(maps.Keys(m)) }

func del(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.Do(t, h, httptest.NewRequest(http.MethodDelete, target, nil))
}

func userPath(id int64, suffix string) string {
	return "/api/v1/admin/users/" + strconv.FormatInt(id, 10) + suffix
}

// findUser returns the listed account id.
func findUser(t *testing.T, h http.Handler, id int64) (dto.User, bool) {
	t.Helper()
	for _, u := range getJSON[dto.Items[dto.User]](t, h, "/api/v1/admin/users").Items {
		if u.ID == id {
			return u, true
		}
	}
	return dto.User{}, false
}

// requireInviteLink requires a 201 whose body is a new invite link for page, expiring ttl after
// seedTime.
func requireInviteLink(t *testing.T, w *httptest.ResponseRecorder, page string, ttl time.Duration) dto.InviteLink {
	t.Helper()
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	l := decodeJSON[dto.InviteLink](t, w)
	assert.True(t, strings.HasPrefix(string(l.URL), testPublicURL+"/"+page+"#token=inv_"), "invite link")
	assert.Equal(t, seedTime.Add(ttl), l.ExpiresAt)
	assert.Empty(t, w.Header().Get("Location"), "no route serves one invite")
	return l
}

func TestAdminUsers(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Admin()

	users := getJSON[dto.Items[dto.User]](t, h, "/api/v1/admin/users").Items
	require.Len(t, users, 2)
	assert.Equal(t, dto.User{ID: seedUser, Username: seedUserName, CreatedAt: seedTime.Add(-time.Hour),
		PasswordChangedAt: seedTime.Add(-time.Hour)}, users[0], "the username is verbatim, not hashed")
	assert.Equal(t, seedOtherUser, users[1].ID)
	assert.Equal(t, dto.Username(seedOtherName), users[1].Username)
	assert.Equal(t, seedTime.Add(-time.Minute), users[1].DisabledAt)

	sessions := getJSON[dto.Items[dto.Session]](t, h, userPath(seedUser, "/sessions")).Items
	assert.Equal(t, []dto.Session{{ID: seedSession, CreatedAt: seedTime.Add(-time.Minute), LastUsedAt: seedTime.Add(-time.Minute),
		ExpiresAt: seedTime.Add(24 * time.Hour)}}, sessions)
	assert.Empty(t, getJSON[dto.Items[dto.Session]](t, h, userPath(seedOtherUser, "/sessions")).Items)
	assert.JSONEq(t, `{"items":[]}`, testutil.Get(t, h, userPath(seedOtherUser, "/sessions")).Body.String(), "[] not null")

	// Disable revokes the sessions; enable clears the disable.
	requireNoContent(t, post(t, h, userPath(seedUser, "/disable")), "disable")
	u, _ := findUser(t, h, seedUser)
	assert.Equal(t, seedTime, u.DisabledAt)
	assert.Empty(t, getJSON[dto.Items[dto.Session]](t, h, userPath(seedUser, "/sessions")).Items, "disable revokes sessions")
	requireNoContent(t, post(t, h, userPath(seedUser, "/disable")), "disable twice")
	requireNoContent(t, post(t, h, userPath(seedOtherUser, "/enable")), "enable")
	u, _ = findUser(t, h, seedOtherUser)
	assert.Zero(t, u.DisabledAt)

	requireNoContent(t, del(t, h, userPath(seedOtherUser, "")), "delete")
	_, ok := findUser(t, h, seedOtherUser)
	assert.False(t, ok, "deleted")

	for _, w := range []*httptest.ResponseRecorder{
		post(t, h, userPath(99, "/disable")),
		post(t, h, userPath(99, "/enable")),
		del(t, h, userPath(seedOtherUser, "")),
		testutil.Get(t, h, userPath(99, "/sessions")),
		postJSON(t, h, userPath(99, "/reset"), `{"scope":"password"}`),
	} {
		require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
		assert.Equal(t, runtime.DetailNoUser.Message(), testutil.DecodeProblem(t, w).Detail)
	}
	testutil.RequireParamProblem(t, post(t, h, "/api/v1/admin/users/x/disable"), dto.ParamID, "bad id")
	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/admin/users?page=1"), dto.ParamPage, "no query params")
}

func TestAdminSessions(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Admin()
	requireNoContent(t, del(t, h, "/api/v1/admin/sessions/1"), "revoke")
	assert.Empty(t, getJSON[dto.Items[dto.Session]](t, h, userPath(seedUser, "/sessions")).Items)
	requireNoContent(t, del(t, h, "/api/v1/admin/sessions/1"), "revoking twice is a no-op")
	w := del(t, h, "/api/v1/admin/sessions/99")
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, runtime.DetailNoSession.Message(), testutil.DecodeProblem(t, w).Detail)
}

func TestAdminInvites(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Admin()

	// The default ttl is auth.invite_ttl (24h when unset).
	created := requireInviteLink(t, postJSON(t, h, "/api/v1/admin/invites", `{}`), "signup", 24*time.Hour)
	assert.Equal(t, seedInvite+1, created.ID)
	requireInviteLink(t, postJSON(t, h, "/api/v1/admin/invites", `{"ttl":"1m"}`), "signup", time.Minute)
	reset := requireInviteLink(t, postJSON(t, h, userPath(seedUser, "/reset"), `{"scope":"totp","ttl":"168h"}`), "reset",
		7*24*time.Hour)

	requireNoContent(t, del(t, h, "/api/v1/admin/invites/1"), "revoke")
	requireNoContent(t, del(t, h, "/api/v1/admin/invites/1"), "revoking twice is a no-op")
	invites := getJSON[dto.Items[dto.Invite]](t, h, "/api/v1/admin/invites").Items
	require.Len(t, invites, 4)
	assert.Equal(t, dto.Invite{ID: reset.ID, Kind: "reset", UserID: seedUser, Scope: "totp", CreatedAt: seedTime,
		ExpiresAt: seedTime.Add(7 * 24 * time.Hour)}, invites[0], "newest first")
	assert.Equal(t, dto.Invite{ID: seedInvite, Kind: "signup", CreatedAt: seedTime.Add(-time.Minute),
		ExpiresAt: seedTime.Add(time.Hour), RevokedAt: seedTime}, invites[3])

	w := del(t, h, "/api/v1/admin/invites/99")
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, runtime.DetailNoInvite.Message(), testutil.DecodeProblem(t, w).Detail)

	// Every invalid member at once; an invalid request creates nothing.
	for body, want := range map[string][]string{
		`{"ttl":"59s"}`:  {"/ttl"},
		`{"ttl":"169h"}`: {"/ttl"},
	} {
		assert.Equal(t, want, keys(problemPointers(t, postJSON(t, h, "/api/v1/admin/invites", body), body)), body)
	}
	for body, want := range map[string][]string{
		`{}`:                          {"/scope"},
		`{"scope":"all","ttl":"-1h"}`: {"/scope", "/ttl"},
	} {
		assert.Equal(t, want, keys(problemPointers(t, postJSON(t, h, userPath(seedUser, "/reset"), body), body)), body)
	}
	assert.Len(t, getJSON[dto.Items[dto.Invite]](t, h, "/api/v1/admin/invites").Items, 4)
}

func TestAdminRotateSigningKey(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	requireNoContent(t, post(t, a.Admin(), "/api/v1/admin/signing-keys/rotate"), "rotate")
	assert.Contains(t, logs.String(), `"event":"signing_key_rotated"`)
}
