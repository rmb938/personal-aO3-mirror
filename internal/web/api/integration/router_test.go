package integration

import (
	"cmp"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	api "github.com/rmb938/personal-aO3-mirror/internal/web/api"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

const testVersion = "1.2.3-test"

func testOptions(t *testing.T) runtime.Options {
	t.Helper()
	return runtime.Options{
		Version:          testVersion,
		TrustedProxies:   []string{"127.0.0.1/32", "::1/128"},
		AllowedHosts:     []string{"example.com"}, // httptest.NewRequest's default Host
		RequestTimeout:   10 * time.Second,
		GeneralPerMin:    1000,
		SourceAddPerHour: 1000,
		SearchPerMin:     1000,
		SignupPerHour:    1000,
		LoginPerMin:      1000,
		RefreshPerMin:    1000,
	}
}

func testNew(t *testing.T, opts runtime.Options, log *slog.Logger) (*runtime.API, error) {
	t.Helper()
	return api.New(opts, log, newTestControllers(t, log))
}

// revealQuery is the query string that asks for revealed content.
const revealQuery = "?" + runtime.RevealParam + "=1"

func TestNewValidatesOptions(t *testing.T) {
	cases := map[string]func(*runtime.Options){
		"no version":    func(o *runtime.Options) { o.Version = "" },
		"no proxies":    func(o *runtime.Options) { o.TrustedProxies = nil },
		"bad proxy":     func(o *runtime.Options) { o.TrustedProxies = []string{"127.0.0.1"} },
		"no hosts":      func(o *runtime.Options) { o.AllowedHosts = nil },
		"bad host":      func(o *runtime.Options) { o.AllowedHosts = []string{"example.com:80"} },
		"no timeout":    func(o *runtime.Options) { o.RequestTimeout = 0 },
		"no rate":       func(o *runtime.Options) { o.GeneralPerMin = 0 },
		"short rotate":  func(o *runtime.Options) { o.HashKeyRotation = time.Minute },
		"negative rate": func(o *runtime.Options) { o.GeneralPerMin = -1 },
		"no add rate":   func(o *runtime.Options) { o.SourceAddPerHour = 0 },
		"no search":     func(o *runtime.Options) { o.SearchPerMin = 0 },
		"no signup":     func(o *runtime.Options) { o.SignupPerHour = 0 },
		"no login":      func(o *runtime.Options) { o.LoginPerMin = 0 },
		"no refresh":    func(o *runtime.Options) { o.RefreshPerMin = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			opts := testOptions(t)
			mutate(&opts)
			log := slog.New(slog.DiscardHandler)
			_, err := api.New(opts, log, newTestControllers(t, log))
			assert.Error(t, err)
		})
	}
	t.Run("nil logger", func(t *testing.T) {
		log := slog.New(slog.DiscardHandler)
		_, err := api.New(testOptions(t), nil, newTestControllers(t, log))
		assert.EqualError(t, err, "api: nil logger")
	})
}

func TestHealthzAndVersion(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	for _, h := range []http.Handler{a.Public(), a.Admin()} {
		w := testutil.Get(t, h, "/api/v1/healthz")
		require.Equal(t, http.StatusOK, w.Code)
		assert.JSONEq(t, `{"status":"ok"}`, w.Body.String())
		assert.Equal(t, "hashed", w.Header().Get(runtime.HeaderFanwork))

		w = testutil.Get(t, h, "/api/v1/version")
		require.Equal(t, http.StatusOK, w.Code)
		var v dto.Version
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
		assert.Equal(t, dto.Version{Version: testVersion, ParserVersion: parse.Version}, v)

		testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/healthz?page=1"), dto.ParamPage, "not taken here")
	}
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	w := testutil.Get(t, a.Public(), "/api/v1/nope")
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, dto.TypeNotFound, testutil.DecodeProblem(t, w).Type)
	assert.Equal(t, "hashed", w.Header().Get(runtime.HeaderFanwork))

	w = testutil.Do(t, a.Public(), httptest.NewRequest(http.MethodDelete, "/api/v1/healthz", nil))
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Equal(t, dto.TypeMethodNotAllowed, testutil.DecodeProblem(t, w).Type)
	assert.Equal(t, []string{http.MethodGet}, w.Header().Values("Allow"))
}

// routeSample is one request the all-routes leak test sends to a route.
type routeSample struct {
	target string
	status int
	// content: the response holds seeded fanwork content, so revealed it must contain a
	// sentinel (the per-route positive control).
	content bool
	// revealStatus is the revealed request's status when it differs from status (a route
	// taking free text is refused hashed).
	revealStatus int
	// body is a JSON request body (sent as application/json), or "" for none.
	body string
	// fresh: the route changes state, so each request goes to a new API over a freshly seeded
	// store (first sent the POSTs in before, e.g. a pause before a resume).
	fresh  bool
	before []string
	// bearer: the route needs an access token. On the public listener every request carries
	// one of seedSession's; on the admin socket every request is 401 (it has no user).
	bearer bool
}

// request builds the sample's request, with suffix appended to its target.
func (s routeSample) request(method, suffix string) *http.Request {
	if s.body == "" {
		return httptest.NewRequest(method, s.target+suffix, nil)
	}
	req := httptest.NewRequest(method, s.target+suffix, strings.NewReader(s.body))
	req.Header.Set("Content-Type", runtime.ContentTypeJSON)
	return req
}

// routeSamples has a sample request for every route, keyed "METHOD pattern". The leak test fails
// when a route has no sample or a sample has no route. Add one with every new route; the ids are
// the seeded rows (seed_test.go).
var routeSamples = map[string]routeSample{
	"GET /api/v1/healthz":      {target: "/api/v1/healthz", status: http.StatusOK},
	"GET /api/v1/version":      {target: "/api/v1/version", status: http.StatusOK},
	"GET /api/v1/sources":      {target: "/api/v1/sources", status: http.StatusOK, content: true},
	"GET /api/v1/sources/{id}": {target: "/api/v1/sources/1", status: http.StatusOK, content: true},
	// Queued on the first listener; the second gets the same job (created false).
	"POST /api/v1/sources": {target: "/api/v1/sources", status: http.StatusAccepted,
		body: `{"url":"https://archiveofourown.org/tags/` + sentinel + `url/works"}`},
	"GET /api/v1/source-adds/{job_id}": {target: "/api/v1/source-adds/1", status: http.StatusOK, content: true},
	"GET /api/v1/jobs":                 {target: "/api/v1/jobs?state=failed&kind=source_add", status: http.StatusOK, content: true},
	"GET /api/v1/jobs/{id}":            {target: "/api/v1/jobs/2", status: http.StatusOK, content: true},
	"GET /api/v1/status":               {target: "/api/v1/status", status: http.StatusOK, content: true},
	"GET /api/v1/works":                {target: "/api/v1/works?sort=title", status: http.StatusOK, content: true},
	"QUERY /api/v1/works": {target: "/api/v1/works?per_page=5", status: http.StatusOK, content: true,
		body: `{"fetched":true,"words":{"min":1},"sort":"title"}`},
	"GET /api/v1/works/{id}":          {target: "/api/v1/works/9", status: http.StatusOK, content: true},
	"GET /api/v1/works/{id}/skin":     {target: "/api/v1/works/9/skin", status: http.StatusOK, content: true},
	"GET /api/v1/works/{id}/chapters": {target: "/api/v1/works/9/chapters", status: http.StatusOK, content: true},
	"GET /api/v1/works/{id}/chapters/{position}": {target: "/api/v1/works/9/chapters/2", status: http.StatusOK,
		content: true},
	"QUERY /api/v1/tags": {target: "/api/v1/tags?per_page=5", status: http.StatusOK, content: true,
		body: `{"types":["Fandom"],"sort":"uses"}`},
	"GET /api/v1/tags/{id}":       {target: "/api/v1/tags/3", status: http.StatusOK, content: true},
	"GET /api/v1/tags/{id}/works": {target: "/api/v1/tags/3/works?sort=title", status: http.StatusOK, content: true},
	"QUERY /api/v1/tags/{id}/works": {target: "/api/v1/tags/3/works?per_page=5", status: http.StatusOK, content: true,
		body: `{"fetched":true,"sort":"title"}`},
	// The name is free text: refused hashed (it never reaches the store), served revealed.
	"GET /api/v1/tags/by-name/{name}": {target: "/api/v1/tags/by-name/" + sentinel + "fandom",
		status: http.StatusBadRequest, revealStatus: http.StatusOK, content: true},
	"GET /api/v1/media": {target: "/api/v1/media", status: http.StatusOK, content: true},
	"GET /api/v1/media/{id}/fandoms": {target: "/api/v1/media/4/fandoms?sort=uses", status: http.StatusOK,
		content: true},
	"GET /api/v1/series/{id}":       {target: "/api/v1/series/77", status: http.StatusOK, content: true},
	"GET /api/v1/series/{id}/works": {target: "/api/v1/series/77/works", status: http.StatusOK, content: true},
	"QUERY /api/v1/creators": {target: "/api/v1/creators?per_page=5", status: http.StatusOK, content: true,
		body: `{"sort":"works"}`},
	"GET /api/v1/creators/{id}":       {target: "/api/v1/creators/1", status: http.StatusOK, content: true},
	"GET /api/v1/creators/{id}/works": {target: "/api/v1/creators/1/works?sort=title", status: http.StatusOK, content: true},
	"QUERY /api/v1/creators/{id}/works": {target: "/api/v1/creators/1/works?per_page=5", status: http.StatusOK,
		content: true, body: `{"fetched":true}`},
	"GET /api/v1/collections/{id}": {target: "/api/v1/collections/1", status: http.StatusOK, content: true},
	"GET /api/v1/collections/{id}/works": {target: "/api/v1/collections/1/works?sort=title", status: http.StatusOK,
		content: true},
	"QUERY /api/v1/collections/{id}/works": {target: "/api/v1/collections/1/works?per_page=5", status: http.StatusOK,
		content: true, body: `{"fetched":true}`},
	"GET /api/v1/images/{sha256}": {target: "/api/v1/images/" + seedImageSHA, status: http.StatusForbidden,
		content: true, revealStatus: http.StatusOK},

	// Admin socket only.
	"POST /api/v1/sources/{id}/confirm": {target: "/api/v1/sources/2/confirm", status: http.StatusNoContent, fresh: true},
	"POST /api/v1/sources/{id}/pause":   {target: "/api/v1/sources/1/pause", status: http.StatusNoContent, fresh: true},
	"POST /api/v1/sources/{id}/resume": {target: "/api/v1/sources/1/resume", status: http.StatusNoContent, fresh: true,
		before: []string{"/api/v1/sources/1/pause"}},
	"POST /api/v1/sources/{id}/cancel": {target: "/api/v1/sources/1/cancel", status: http.StatusOK, fresh: true},
	"POST /api/v1/sources/{id}/refresh": {target: "/api/v1/sources/1/refresh", status: http.StatusOK, fresh: true,
		body: `{"full":true}`},
	"PATCH /api/v1/sources/{id}": {target: "/api/v1/sources/1", status: http.StatusNoContent, fresh: true,
		body: `{"poll_interval":"2h","priority":5}`},
	"DELETE /api/v1/sources/{id}":  {target: "/api/v1/sources/1", status: http.StatusNoContent, fresh: true},
	"GET /api/v1/debug/jobs/{id}":  {target: "/api/v1/debug/jobs/4", status: http.StatusOK, content: true},
	"GET /api/v1/debug/works/{id}": {target: "/api/v1/debug/works/7", status: http.StatusOK, content: true},
	// Accounts (seed_auth_test.go): no fanwork, so no revealed control.
	"POST /api/v1/admin/invites": {target: "/api/v1/admin/invites", status: http.StatusCreated, fresh: true,
		body: `{"ttl":"1h"}`},
	"GET /api/v1/admin/invites":         {target: "/api/v1/admin/invites", status: http.StatusOK},
	"DELETE /api/v1/admin/invites/{id}": {target: "/api/v1/admin/invites/1", status: http.StatusNoContent, fresh: true},
	"GET /api/v1/admin/users":           {target: "/api/v1/admin/users", status: http.StatusOK},
	"DELETE /api/v1/admin/users/{id}":   {target: "/api/v1/admin/users/2", status: http.StatusNoContent, fresh: true},
	"POST /api/v1/admin/users/{id}/disable": {target: "/api/v1/admin/users/1/disable", status: http.StatusNoContent,
		fresh: true},
	"POST /api/v1/admin/users/{id}/enable": {target: "/api/v1/admin/users/2/enable", status: http.StatusNoContent,
		fresh: true},
	"POST /api/v1/admin/users/{id}/reset": {target: "/api/v1/admin/users/1/reset", status: http.StatusCreated,
		fresh: true, body: `{"scope":"both","ttl":"2h"}`},
	"GET /api/v1/admin/users/{id}/sessions": {target: "/api/v1/admin/users/1/sessions", status: http.StatusOK},
	"DELETE /api/v1/admin/sessions/{id}":    {target: "/api/v1/admin/sessions/1", status: http.StatusNoContent, fresh: true},
	"POST /api/v1/admin/signing-keys/rotate": {target: "/api/v1/admin/signing-keys/rotate", status: http.StatusNoContent,
		fresh: true},
	// Auth entry points over the seeded invites and pending enrollments.
	"POST /api/v1/auth/signup": {target: "/api/v1/auth/signup", status: http.StatusOK, fresh: true,
		body: `{"invite_token":"` + seedInviteToken + `","username":"Fixture_Signup","password":"fixture correct horse"}`},
	"POST /api/v1/auth/signup/confirm": {target: "/api/v1/auth/signup/confirm", status: http.StatusCreated, fresh: true,
		body: `{"enrollment_token":"` + seedSignupEnrollToken + `","code":"` + seedTOTPCode + `"}`},
	"POST /api/v1/auth/reset": {target: "/api/v1/auth/reset", status: http.StatusOK, fresh: true,
		body: `{"invite_token":"` + seedResetToken + `"}`},
	"POST /api/v1/auth/reset/confirm": {target: "/api/v1/auth/reset/confirm", status: http.StatusOK, fresh: true,
		body: `{"enrollment_token":"` + seedResetEnrollToken + `","code":"` + seedTOTPCode + `"}`},
	"POST /api/v1/auth/login": {target: "/api/v1/auth/login", status: http.StatusOK, fresh: true,
		body: `{"username":"` + seedUserName + `","password":"` + fixturePassword + `"}`},
	"POST /api/v1/auth/login/totp": {target: "/api/v1/auth/login/totp", status: http.StatusOK, fresh: true,
		body: `{"challenge_token":"` + seedChallengeToken + `","code":"` + seedTOTPCode + `"}`},
	"POST /api/v1/auth/refresh": {target: "/api/v1/auth/refresh", status: http.StatusOK, fresh: true,
		body: `{"refresh_token":"` + seedRefreshToken + `"}`},
	// The signed-in account, as seedUser in seedSession.
	"POST /api/v1/auth/logout": {target: "/api/v1/auth/logout", status: http.StatusNoContent, fresh: true, bearer: true},
	"GET /api/v1/account":      {target: "/api/v1/account", status: http.StatusOK, bearer: true},
	"PUT /api/v1/account/password": {target: "/api/v1/account/password", status: http.StatusNoContent, fresh: true,
		bearer: true, body: `{"current_password":"` + fixturePassword + `","password":"fixture battery staple","code":"` +
			seedTOTPCode + `"}`},
	"POST /api/v1/account/recovery-codes": {target: "/api/v1/account/recovery-codes", status: http.StatusOK, fresh: true,
		bearer: true, body: `{"code":"` + seedTOTPCode + `"}`},
	"GET /api/v1/account/sessions": {target: "/api/v1/account/sessions", status: http.StatusOK, bearer: true},
	"DELETE /api/v1/account/sessions/{id}": {target: "/api/v1/account/sessions/1", status: http.StatusNoContent,
		fresh: true, bearer: true},
}

// sentinel marks seeded content: no response or log line may contain it in hashed mode.
const sentinel = "SENTINEL-"

func walkRoutes(t *testing.T, r chi.Routes) []string {
	t.Helper()
	var out []string
	require.NoError(t, chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out = append(out, method+" "+route)
		return nil
	}))
	slices.Sort(out)
	return out
}

// TestAllRoutesLeak is the all-routes leak test (docs/part2-api.md, "Fanwork protection" 7). It
// calls every route on both listeners in hashed mode and asserts that no seeded sentinel appears
// in the body, the headers or the logs, also when the request itself carries sentinels in
// places the server must not echo. Store-backed routes (stage 3 on) seed a temp store where
// every text field is SENTINEL-<field>.
func TestAllRoutesLeak(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	sharedToken := "" // an access token on a's public router, made at first use
	for name, r := range map[string]*chi.Mux{"public": a.Router(false), "admin": a.Router(true)} {
		routes := walkRoutes(t, r)
		for _, rt := range routes {
			assert.Contains(t, routeSamples, rt, "%s route has no sample in routeSamples", name)
		}
		if name == "admin" {
			for key := range routeSamples {
				assert.Contains(t, routes, key, "sample has no admin route")
			}
		}
		for _, rt := range routes {
			s, ok := routeSamples[rt]
			if !ok {
				continue
			}
			method, _, _ := strings.Cut(rt, " ")
			t.Run(name+" "+rt, func(t *testing.T) {
				// to is the router a request goes to: r, or for a fresh sample a new one.
				to := func() http.Handler {
					if !s.fresh {
						return r
					}
					opts := testOptions(t)
					log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
					b, err := api.New(opts, log, newTestControllers(t, log))
					require.NoError(t, err)
					h := b.Router(false)
					if name == "admin" {
						h = b.Router(true)
					}
					for _, target := range s.before {
						require.Less(t, testutil.Do(t, h, httptest.NewRequest(http.MethodPost, target, nil)).Code, 300, target)
					}
					return h
				}
				// send sends req to to(), with an access token for a bearer route on the
				// public listener.
				send := func(req *http.Request) *httptest.ResponseRecorder {
					h := to()
					if s.bearer && name == "public" {
						tok := sharedToken
						switch {
						case s.fresh:
							tok = accessToken(t, h)
						case tok == "":
							tok = accessToken(t, h)
							sharedToken = tok
						}
						req.Header.Set("Authorization", "Bearer "+tok)
					}
					return testutil.Do(t, h, req)
				}
				noUser := s.bearer && name == "admin"
				w := send(s.request(method, ""))
				if noUser {
					assert.Equal(t, http.StatusUnauthorized, w.Code)
				} else {
					assert.Equal(t, s.status, w.Code)
				}
				assert.Equal(t, "hashed", w.Header().Get(runtime.HeaderFanwork))
				testutil.AssertNoSentinel(t, w, logs, sentinel)

				// Hostile input: sentinels in a param name and value, the request ID and the
				// body are never echoed or logged.
				sep := "?"
				if strings.Contains(s.target, "?") {
					sep = "&"
				}
				req := httptest.NewRequest(method, s.target+sep+sentinel+"name="+sentinel+"value",
					strings.NewReader(`{"x":"`+sentinel+`body", "`+sentinel+`key":1}`))
				req.Header.Set(runtime.HeaderRequestID, sentinel+" not a valid id")
				if s.body != "" {
					req.Header.Set("Content-Type", runtime.ContentTypeJSON)
				}
				w = send(req)
				switch {
				case noUser:
					// Refused before the parameters and body are read.
					require.Equal(t, http.StatusUnauthorized, w.Code)
					assert.Equal(t, dto.TypeUnauthorized, testutil.DecodeProblem(t, w).Type)
				case s.body == "":
					testutil.RequireParamProblem(t, w, "", "unknown param, never echoed")
				default:
					// An unreadable body is reported before the parameters.
					require.Equal(t, http.StatusBadRequest, w.Code)
					assert.Equal(t, dto.TypeInvalidBody, testutil.DecodeProblem(t, w).Type)
				}
				testutil.AssertNoSentinel(t, w, logs, sentinel)

				if s.content {
					w = testutil.Do(t, to(), s.request(method, sep+runtime.RevealParam+"=1"))
					assert.Equal(t, cmp.Or(s.revealStatus, s.status), w.Code)
					assert.Equal(t, "revealed", w.Header().Get(runtime.HeaderFanwork))
					assert.Contains(t, w.Body.String(), sentinel, "revealed response holds the seeded content")
					assert.NotContains(t, logs.String(), sentinel, "revealed content is never logged")
				}
			})
		}
	}
}

// TestLeakHarnessDetectsContent is the positive control: a route that returns a sentinel is
// hashed by default and returns the sentinel only when revealed, so the harness can see a leak.
func TestLeakHarnessDetectsContent(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	a.Router(false).Get("/leaktest", a.Handle(func(*http.Request) (any, error) {
		return dto.Health{Status: sentinel + "status"}, nil
	}))

	w := testutil.Get(t, a.Router(false), "/leaktest")
	require.Equal(t, http.StatusOK, w.Code)
	testutil.AssertNoSentinel(t, w, logs, sentinel)
	var h dto.Health
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &h))
	assert.True(t, strings.HasPrefix(string(h.Status), "h_"), "hashed")
	assert.Contains(t, logs.String(), "enum value outside its set", "off-set enum value is warned about")

	w = testutil.Get(t, a.Router(false), "/leaktest"+revealQuery)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "revealed", w.Header().Get(runtime.HeaderFanwork))
	assert.Contains(t, w.Body.String(), sentinel+"status")
	assert.NotContains(t, logs.String(), sentinel, "revealed content is still never logged")
}

// TestEnumScopedToField checks that an enum field allows only its own type's values: every
// other DTO enum value (a WorkStatus, a JobKind, ...) in dto.Health.Status is hashed and
// warned about, while each type's own values pass in their own fields.
func TestEnumScopedToField(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	var value string
	a.Router(false).Get("/enum", a.Handle(func(*http.Request) (any, error) {
		return dto.Health{Status: dto.HealthStatus(value)}, nil
	}))
	others := slices.Concat(dto.WorkStatus("").EnumValues(), dto.ImageStatus("").EnumValues(),
		dto.JobKind("").EnumValues(), dto.JobState("").EnumValues(), dto.SourceKind("").EnumValues(),
		dto.SourceState("").EnumValues(), dto.RequestKind("").EnumValues())
	for _, v := range others {
		if slices.Contains(dto.HealthStatus("").EnumValues(), v) {
			continue
		}
		value = v
		w := testutil.Get(t, a.Router(false), "/enum")
		require.Equal(t, http.StatusOK, w.Code)
		var h dto.Health
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &h))
		assert.True(t, strings.HasPrefix(string(h.Status), "h_"), "%q hashed in dto.Health.Status", v)
	}
	assert.Contains(t, logs.String(), "dto.Health.Status", "off-set enum warning names the field")

	value = string(dto.HealthOK)
	assert.JSONEq(t, `{"status":"ok"}`, testutil.Get(t, a.Router(false), "/enum").Body.String())
}

// TestPathParamsDeclared: every route's path parameter is an id the access log records
// (runtime.PathIDParams) or one it never logs (a free-text name, an image hash), so a new
// parameter needs that decision first.
func TestPathParamsDeclared(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	freeText := []dto.ParamName{dto.ParamTagName, dto.ParamSHA256}
	for _, rt := range walkRoutes(t, a.Router(true)) {
		for seg := range strings.SplitSeq(rt, "/") {
			name, ok := strings.CutPrefix(seg, "{")
			if !ok {
				continue
			}
			p := dto.ParamName(strings.TrimSuffix(name, "}"))
			assert.True(t, slices.Contains(runtime.PathIDParams, p) || slices.Contains(freeText, p),
				"%s: path param %q is neither a logged id nor declared unlogged", rt, p)
		}
	}
}
