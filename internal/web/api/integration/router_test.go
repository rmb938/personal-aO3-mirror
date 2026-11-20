package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	api "github.com/rmb938/personal-aO3-mirror/internal/web/api"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

const testVersion = "1.2.3-test"

// logLines returns the JSON log records.
func logLines(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.Lines(logs.String()) {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m))
		out = append(out, m)
	}
	return out
}

// accessLine returns the single access log record.
func accessLine(t *testing.T, logs *bytes.Buffer) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, l := range logLines(t, logs) {
		if l["msg"] == "api request" {
			found = append(found, l)
		}
	}
	require.Len(t, found, 1)
	return found[0]
}

// testOptions are valid options logging JSON to logs, with controllers over a seeded temp
// store (seed_test.go).
func testOptions(t *testing.T, logs io.Writer) runtime.Options {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return runtime.Options{
		Logger:           log,
		Controllers:      newTestControllers(t, log),
		Version:          testVersion,
		TrustedProxies:   []string{"127.0.0.1/32", "::1/128"},
		RequestTimeout:   10 * time.Second,
		GeneralPerMin:    1000,
		SourceAddPerHour: 1000,
	}
}

// newTestAPI builds an API logging JSON to the returned buffer. mutate may change the options.
func newTestAPI(t *testing.T, mutate func(*runtime.Options)) (*runtime.API, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	opts := testOptions(t, logs)
	if mutate != nil {
		mutate(&opts)
	}
	a, err := api.New(opts)
	require.NoError(t, err)
	return a, logs
}

// do sends req to h and returns the recorded response.
func do(t *testing.T, h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, h, httptest.NewRequest(http.MethodGet, target, nil))
}

func decodeProblem(t *testing.T, w *httptest.ResponseRecorder) dto.Problem {
	t.Helper()
	assert.Equal(t, runtime.ContentTypeProblem, w.Header().Get("Content-Type"))
	var p dto.Problem
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	assert.Equal(t, w.Code, p.Status)
	return p
}

// requireParamProblem asserts w is a 422 validation error with one error naming param (""
// for an undeclared name, which is never echoed).
func requireParamProblem(t *testing.T, w *httptest.ResponseRecorder, param dto.ParamName, msg string) {
	t.Helper()
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, msg)
	p := decodeProblem(t, w)
	assert.Equal(t, dto.TypeValidation, p.Type, msg)
	require.Len(t, p.Errors, 1, msg)
	assert.Equal(t, param, p.Errors[0].Parameter, msg)
	assert.NotEmpty(t, p.Errors[0].Detail, msg)
}

// revealQuery is the query string that asks for revealed content.
const revealQuery = "?" + runtime.RevealParam + "=1"

func TestNewValidatesOptions(t *testing.T) {
	cases := map[string]func(*runtime.Options){
		"nil logger":    func(o *runtime.Options) { o.Logger = nil },
		"nil ctl":       func(o *runtime.Options) { o.Controllers = nil },
		"no version":    func(o *runtime.Options) { o.Version = "" },
		"no proxies":    func(o *runtime.Options) { o.TrustedProxies = nil },
		"bad proxy":     func(o *runtime.Options) { o.TrustedProxies = []string{"127.0.0.1"} },
		"no timeout":    func(o *runtime.Options) { o.RequestTimeout = 0 },
		"no rate":       func(o *runtime.Options) { o.GeneralPerMin = 0 },
		"short rotate":  func(o *runtime.Options) { o.HashKeyRotation = time.Minute },
		"negative rate": func(o *runtime.Options) { o.GeneralPerMin = -1 },
		"no add rate":   func(o *runtime.Options) { o.SourceAddPerHour = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			opts := testOptions(t, io.Discard)
			mutate(&opts)
			_, err := api.New(opts)
			assert.Error(t, err)
		})
	}
}

func TestHealthzAndVersion(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	for _, h := range []http.Handler{a.Public(), a.Admin()} {
		w := get(t, h, "/api/v1/healthz")
		require.Equal(t, http.StatusOK, w.Code)
		assert.JSONEq(t, `{"status":"ok"}`, w.Body.String())
		assert.Equal(t, "hashed", w.Header().Get(runtime.HeaderFanwork))

		w = get(t, h, "/api/v1/version")
		require.Equal(t, http.StatusOK, w.Code)
		var v dto.Version
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
		assert.Equal(t, dto.Version{Version: testVersion, ParserVersion: parse.Version}, v)

		requireParamProblem(t, get(t, h, "/api/v1/healthz?page=1"), dto.ParamPage, "not taken here")
	}
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	w := get(t, a.Public(), "/api/v1/nope")
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, dto.TypeNotFound, decodeProblem(t, w).Type)
	assert.Equal(t, "hashed", w.Header().Get(runtime.HeaderFanwork))

	w = do(t, a.Public(), httptest.NewRequest(http.MethodDelete, "/api/v1/healthz", nil))
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Equal(t, dto.TypeMethodNotAllowed, decodeProblem(t, w).Type)
	assert.Equal(t, []string{http.MethodGet}, w.Header().Values("Allow"))
}

// routeSample is one request the all-routes leak test sends to a route.
type routeSample struct {
	target string
	status int
	// content: the response holds seeded fanwork content, so revealed it must contain a
	// sentinel (the per-route positive control).
	content bool
	// body is a JSON request body (sent as application/json), or "" for none.
	body string
	// fresh: the route changes state, so each request goes to a new API over a freshly seeded
	// store (first sent the POSTs in before, e.g. a pause before a resume).
	fresh  bool
	before []string
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

// assertNoSentinel checks a response and the logs for seeded content.
func assertNoSentinel(t *testing.T, w *httptest.ResponseRecorder, logs *bytes.Buffer) {
	t.Helper()
	assert.NotContains(t, w.Body.String(), sentinel, "body")
	for k, vs := range w.Header() {
		for _, v := range vs {
			assert.NotContains(t, v, sentinel, "header %s", k)
		}
	}
	assert.NotContains(t, logs.String(), sentinel, "logs")
}

// TestAllRoutesLeak is the all-routes leak test (docs/part2-api.md, "Fanwork protection" 7). It
// calls every route on both listeners in hashed mode and asserts that no seeded sentinel appears
// in the body, the headers or the logs, also when the request itself carries sentinels in
// places the server must not echo. Store-backed routes (stage 3 on) seed a temp store where
// every text field is SENTINEL-<field>.
func TestAllRoutesLeak(t *testing.T) {
	a, logs := newTestAPI(t, nil)
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
					b, err := api.New(testOptions(t, logs))
					require.NoError(t, err)
					h := b.Router(false)
					if name == "admin" {
						h = b.Router(true)
					}
					for _, target := range s.before {
						require.Less(t, do(t, h, httptest.NewRequest(http.MethodPost, target, nil)).Code, 300, target)
					}
					return h
				}
				w := do(t, to(), s.request(method, ""))
				assert.Equal(t, s.status, w.Code)
				assert.Equal(t, "hashed", w.Header().Get(runtime.HeaderFanwork))
				assertNoSentinel(t, w, logs)

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
				w = do(t, to(), req)
				if s.body == "" {
					requireParamProblem(t, w, "", "unknown param, never echoed")
				} else {
					// An unreadable body is reported before the parameters.
					require.Equal(t, http.StatusBadRequest, w.Code)
					assert.Equal(t, dto.TypeInvalidBody, decodeProblem(t, w).Type)
				}
				assertNoSentinel(t, w, logs)

				if s.content {
					w = do(t, to(), s.request(method, sep+runtime.RevealParam+"=1"))
					assert.Equal(t, s.status, w.Code)
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
	a, logs := newTestAPI(t, nil)
	a.Router(false).Get("/leaktest", a.Handle(func(*http.Request) (any, error) {
		return dto.Health{Status: sentinel + "status"}, nil
	}))

	w := get(t, a.Router(false), "/leaktest")
	require.Equal(t, http.StatusOK, w.Code)
	assertNoSentinel(t, w, logs)
	var h dto.Health
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &h))
	assert.True(t, strings.HasPrefix(string(h.Status), "h_"), "hashed")
	assert.Contains(t, logs.String(), "enum value outside its set", "off-set enum value is warned about")

	w = get(t, a.Router(false), "/leaktest"+revealQuery)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "revealed", w.Header().Get(runtime.HeaderFanwork))
	assert.Contains(t, w.Body.String(), sentinel+"status")
	assert.NotContains(t, logs.String(), sentinel, "revealed content is still never logged")
}

// TestEnumScopedToField checks that an enum field allows only its own type's values: every
// other DTO enum value (a WorkStatus, a JobKind, ...) in dto.Health.Status is hashed and
// warned about, while each type's own values pass in their own fields.
func TestEnumScopedToField(t *testing.T) {
	a, logs := newTestAPI(t, nil)
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
		w := get(t, a.Router(false), "/enum")
		require.Equal(t, http.StatusOK, w.Code)
		var h dto.Health
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &h))
		assert.True(t, strings.HasPrefix(string(h.Status), "h_"), "%q hashed in dto.Health.Status", v)
	}
	assert.Contains(t, logs.String(), "dto.Health.Status", "off-set enum warning names the field")

	value = string(dto.HealthOK)
	assert.JSONEq(t, `{"status":"ok"}`, get(t, a.Router(false), "/enum").Body.String())
}

// openAPISpec is the part of docs/api/openapi.yaml the coverage test reads.
type openAPISpec struct {
	Paths map[string]map[string]yaml.Node `yaml:"paths"`
}

var openAPIMethods = map[string]string{
	"get": http.MethodGet, "put": http.MethodPut, "post": http.MethodPost, "delete": http.MethodDelete,
	"patch": http.MethodPatch, "query": runtime.MethodQuery,
}

// TestRoutesMatchOpenAPI checks docs/api/openapi.yaml against the routers: the public router has
// exactly the operations not marked x-admin-only, and the admin router has all of them.
func TestRoutesMatchOpenAPI(t *testing.T) {
	raw, err := os.ReadFile("../../../../docs/api/openapi.yaml")
	require.NoError(t, err)
	var spec openAPISpec
	require.NoError(t, yaml.Unmarshal(raw, &spec))

	var public, all []string
	for path, item := range spec.Paths {
		for key, node := range item {
			method, ok := openAPIMethods[key]
			if !ok {
				continue
			}
			var op struct {
				AdminOnly bool `yaml:"x-admin-only"`
			}
			require.NoError(t, node.Decode(&op))
			rt := method + " " + path
			all = append(all, rt)
			if !op.AdminOnly {
				public = append(public, rt)
			}
		}
	}
	slices.Sort(public)
	slices.Sort(all)
	require.NotEmpty(t, public)

	a, _ := newTestAPI(t, nil)
	assert.Equal(t, public, walkRoutes(t, a.Router(false)), "public routes vs openapi.yaml")
	assert.Equal(t, all, walkRoutes(t, a.Router(true)), "admin routes vs openapi.yaml")
}
