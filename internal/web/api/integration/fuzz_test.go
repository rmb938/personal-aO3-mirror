package integration

import (
	"bytes"
	"encoding/json/jsontext"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

// fuzzTarget is a route FuzzRoutes sends input to: the method, the path (with "%s" for a
// fuzzed path segment, if any) and whether it goes to the admin socket.
type fuzzTarget struct {
	method string
	path   string
	admin  bool
}

var fuzzTargets = []fuzzTarget{
	{runtime.MethodQuery, route.Works, false},
	{runtime.MethodQuery, route.Tags, false},
	{runtime.MethodQuery, route.Creators, false},
	{runtime.MethodQuery, route.Path(route.TagWorks, seedTag), false},
	{runtime.MethodQuery, route.Path(route.CreatorWorks, seedCreator), false},
	{runtime.MethodQuery, route.Path(route.CollectionWorks, seedCollection), false},
	{http.MethodGet, route.Works, false},
	{http.MethodGet, route.Path(route.TagWorks, seedTag), false},
	{http.MethodGet, route.Jobs, false},
	{http.MethodGet, "/api/v1/tags/by-name/%s", false},
	{http.MethodGet, "/api/v1/images/%s", false},
	{http.MethodPost, route.Sources, true},
	{http.MethodPatch, route.Path(route.Source, seedTagSource), true},
	// Stage 16: the auth entry points, the signed-in account and the admin user routes that
	// take a body.
	{http.MethodPost, route.AuthSignup, false},
	{http.MethodPost, route.AuthSignupConfirm, false},
	{http.MethodPost, route.AuthReset, false},
	{http.MethodPost, route.AuthResetConfirm, false},
	{http.MethodPost, route.AuthLogin, false},
	{http.MethodPost, route.AuthLoginTOTP, false},
	{http.MethodPost, route.AuthRefresh, false},
	{http.MethodPut, route.AccountPassword, false},
	{http.MethodPost, route.AccountRecoveryCodes, false},
	{http.MethodPost, route.AdminInvites, true},
	{http.MethodPost, route.Path(route.AdminUserReset, seedUser), true},
}

// fuzzAuthBodies are stage 16 request bodies for FuzzRoutes: the seeded tokens and codes (so
// some reach the store), near misses, and the sentinel as a password (which must never reach
// a response or the logs).
func fuzzAuthBodies() []string {
	return []string{
		`{"invite_token":"` + seedInviteToken + `","username":"Fixture_Fuzz","password":"` + sentinel + `fixture pass"}`,
		`{"invite_token":"` + seedResetToken + `","password":"` + sentinel + `"}`,
		`{"enrollment_token":"` + seedSignupEnrollToken + `","code":"` + seedTOTPCode + `"}`,
		`{"enrollment_token":"` + seedResetEnrollToken + `","code":"000000"}`,
		`{"username":"` + seedUserName + `","password":"` + fixturePassword + `"}`,
		`{"username":"` + seedOtherName + `","password":"` + sentinel + `"}`,
		`{"challenge_token":"` + seedChallengeToken + `","code":"` + seedTOTPCode + `"}`,
		`{"challenge_token":"` + seedChallengeToken + `","recovery_code":" FIXT-UREC-OVRY-CODE"}`,
		`{"challenge_token":"chl_` + sentinel + `","code":"123456","recovery_code":"` + sentinel + `"}`,
		`{"refresh_token":"` + seedRefreshToken + `"}`, `{"refresh_token":"rt_` + sentinel + `"}`,
		`{"current_password":"` + fixturePassword + `","password":"` + sentinel + `new pass","code":"` + seedTOTPCode + `"}`,
		`{"code":"` + seedTOTPCode + `"}`, `{"ttl":"1h"}`, `{"scope":"both","ttl":"168h"}`, `{"scope":"totp","ttl":"1s"}`,
	}
}

// FuzzRoutes sends fuzzed query strings, bodies and path segments to the routes that parse
// client input, over the seeded store, hashed and revealed. No input may cause a server error
// (a valid request the store refuses, or an invalid one that gets past validation); every
// response is JSON; hashed, no seeded sentinel reaches the response; and nothing reaches the
// logs.
func FuzzRoutes(f *testing.F) {
	for k := range fuzzTargets {
		for _, s := range []struct{ query, body, seg string }{
			{"", `{}`, "fixture"},
			{"page=2&per_page=1", `{"q":"` + sentinel + ` -lorem \"ipsum dolor\"","sort":"best_match"}`, "Fixture*s*Tag"},
			{"sort=kudos&direction=asc", `{"q":"\"","tag_ids":[1,2,3],"exclude_tag_ids":[4],"statuses":["fetched"]}`, "%zz"},
			{"state=queued&kind=work", `{"q":"NEAR(a b) OR c* ^d AND {e}:f","language":"` + sentinel + `"}`, "a/b"},
			{"page=5001&per_page=20", `{"words":{"min":0,"max":2147483647},"revised":{"from":"2020-01-01","to":"2020-12-31"}}`, ""},
			{"x=" + sentinel, `{"name":"` + sentinel + ` tag","types":["fandom"],"parent_ids":[1],"canonical":false}`, " "},
			{"", `{"name":"\" * - ( ) :","sort":"works","direction":"desc"}`, seedImageSHA},
			{"", `{"url":"https://archiveofourown.org/tags/` + sentinel + `/works","order":"kudos_desc","priority":3}`, "*d**d*"},
			{"", `{"priority":-1000,"poll_interval":"720h"}`, strings.Repeat("a", 300)},
			{"", `null`, "\x00"},
		} {
			f.Add(uint8(k), s.query, []byte(s.body), s.seg, k%2 == 0)
		}
		if strings.Contains(fuzzTargets[k].path, "/auth/") || strings.Contains(fuzzTargets[k].path, "/account/") ||
			strings.Contains(fuzzTargets[k].path, "/admin/") {
			for i, b := range fuzzAuthBodies() {
				f.Add(uint8(k), "", []byte(b), "", i%2 == 0)
			}
		}
	}
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	opts := runtime.Options{ // testOptions with limits too high to trip
		Version: testVersion, TrustedProxies: []string{"127.0.0.1/32", "::1/128"}, AllowedHosts: []string{"example.com"},
		RequestTimeout: 10 * time.Second,
		GeneralPerMin:  math.MaxInt32, SourceAddPerHour: math.MaxInt32, SearchPerMin: math.MaxInt32,
		SignupPerHour: math.MaxInt32, LoginPerMin: math.MaxInt32, RefreshPerMin: math.MaxInt32,
	}
	a, err := newAPI(f, opts, log, newTestControllers(f, log))
	require.NoError(f, err)
	public := signedIn(f, a)

	f.Fuzz(func(t *testing.T, kind uint8, query string, body []byte, seg string, reveal bool) {
		logs.Reset()
		tg := fuzzTargets[int(kind)%len(fuzzTargets)]
		r := httptest.NewRequest(tg.method, "/", bytes.NewReader(body))
		r.Header.Set("Content-Type", runtime.ContentTypeJSON)
		r.URL.Path, r.URL.RawPath = tg.path, ""
		if strings.Contains(tg.path, "%s") {
			r.URL.Path = strings.Replace(tg.path, "%s", seg, 1)
			r.URL.RawPath = strings.Replace(tg.path, "%s", url.PathEscape(seg), 1)
		}
		r.URL.RawQuery = query
		if reveal {
			if r.URL.RawQuery != "" {
				r.URL.RawQuery += "&"
			}
			r.URL.RawQuery += runtime.RevealParam + "=1"
		}
		h := public
		if tg.admin {
			h = a.Admin()
		}
		w := testutil.Do(t, h, r)

		require.Less(t, w.Code, 500, "%s %s?%s %q: %d %s", tg.method, r.URL.RawPath, r.URL.RawQuery, body, w.Code, w.Body.String())
		if tg.path == "/api/v1/images/%s" && w.Code == http.StatusOK {
			require.True(t, reveal)
		} else if w.Code != http.StatusNoContent {
			require.True(t, jsontext.Value(w.Body.Bytes()).IsValid(), "status %d: body is JSON", w.Code)
		}
		if !reveal {
			require.Equal(t, "hashed", w.Header().Get(runtime.HeaderFanwork))
			require.NotContains(t, w.Body.String(), sentinel)
			for k, vs := range w.Header() {
				for _, v := range vs {
					require.NotContains(t, v, sentinel, k)
				}
			}
		}
		require.NotContains(t, logs.String(), sentinel, "status "+strconv.Itoa(w.Code))
	})
}
