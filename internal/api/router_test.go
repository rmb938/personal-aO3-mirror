package api

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
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
)

const testVersion = "1.2.3-test"

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// lines returns the JSON log records.
func (s *syncBuffer) lines(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.Lines(s.String()) {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m))
		out = append(out, m)
	}
	return out
}

// accessLine returns the single access log record.
func (s *syncBuffer) accessLine(t *testing.T) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, l := range s.lines(t) {
		if l["msg"] == "api request" {
			found = append(found, l)
		}
	}
	require.Len(t, found, 1)
	return found[0]
}

// testOptions are valid options logging JSON to logs, with controllers over a seeded temp
// store (seed_test.go).
func testOptions(t *testing.T, logs io.Writer) Options {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return Options{
		Logger:         log,
		Controllers:    newTestControllers(t, log),
		Version:        testVersion,
		TrustedProxies: []string{"127.0.0.1/32", "::1/128"},
		RequestTimeout: 10 * time.Second,
		GeneralPerMin:  1000,
	}
}

// newTestAPI builds an API logging JSON to the returned buffer. mutate may change the options.
func newTestAPI(t *testing.T, mutate func(*Options)) (*API, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	opts := testOptions(t, logs)
	if mutate != nil {
		mutate(&opts)
	}
	a, err := New(opts)
	require.NoError(t, err)
	return a, logs
}

// do sends req to h and returns the recorded response.
func do(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func get(h http.Handler, target string) *httptest.ResponseRecorder {
	return do(h, httptest.NewRequest(http.MethodGet, target, nil))
}

func decodeProblem(t *testing.T, w *httptest.ResponseRecorder) dto.Problem {
	t.Helper()
	assert.Equal(t, contentTypeProblem, w.Header().Get("Content-Type"))
	var p dto.Problem
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	assert.Equal(t, w.Code, p.Status)
	return p
}

// revealQuery is the query string that asks for revealed content.
const revealQuery = "?" + RevealParam + "=1"

func TestNewValidatesOptions(t *testing.T) {
	cases := map[string]func(*Options){
		"nil logger":    func(o *Options) { o.Logger = nil },
		"nil ctl":       func(o *Options) { o.Controllers = nil },
		"no version":    func(o *Options) { o.Version = "" },
		"no proxies":    func(o *Options) { o.TrustedProxies = nil },
		"bad proxy":     func(o *Options) { o.TrustedProxies = []string{"127.0.0.1"} },
		"no timeout":    func(o *Options) { o.RequestTimeout = 0 },
		"no rate":       func(o *Options) { o.GeneralPerMin = 0 },
		"short rotate":  func(o *Options) { o.HashKeyRotation = time.Minute },
		"negative rate": func(o *Options) { o.GeneralPerMin = -1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			opts := testOptions(t, io.Discard)
			mutate(&opts)
			_, err := New(opts)
			assert.Error(t, err)
		})
	}
}

func TestHealthzAndVersion(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	for _, h := range []http.Handler{a.Public(), a.Admin()} {
		w := get(h, "/api/v1/healthz")
		require.Equal(t, http.StatusOK, w.Code)
		assert.JSONEq(t, `{"status":"ok"}`, w.Body.String())
		assert.Equal(t, "hashed", w.Header().Get(headerFanwork))

		w = get(h, "/api/v1/version")
		require.Equal(t, http.StatusOK, w.Code)
		var v dto.Version
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
		assert.Equal(t, dto.Version{Version: testVersion, ParserVersion: parse.Version}, v)

		w = get(h, "/api/v1/healthz?page=1")
		require.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, dto.CodeUnknownParam, decodeProblem(t, w).Code)
	}
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	w := get(a.Public(), "/api/v1/nope")
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, dto.CodeNotFound, decodeProblem(t, w).Code)
	assert.Equal(t, "hashed", w.Header().Get(headerFanwork))

	w = do(a.Public(), httptest.NewRequest(http.MethodDelete, "/api/v1/healthz", nil))
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Equal(t, dto.CodeMethodNotAllowed, decodeProblem(t, w).Code)
	assert.Equal(t, []string{http.MethodGet}, w.Header().Values("Allow"))
}

// routeSample is one request the all-routes leak test sends to a route.
type routeSample struct {
	target string
	status int
	// content: the response holds seeded fanwork content, so revealed it must contain a
	// sentinel (the per-route positive control).
	content bool
}

// routeSamples has a sample request for every route, keyed "METHOD pattern". The leak test fails
// when a route has no sample or a sample has no route. Add one with every new route; the ids are
// the seeded rows (seed_test.go).
var routeSamples = map[string]routeSample{
	"GET /api/v1/healthz":              {target: "/api/v1/healthz", status: http.StatusOK},
	"GET /api/v1/version":              {target: "/api/v1/version", status: http.StatusOK},
	"GET /api/v1/sources":              {target: "/api/v1/sources", status: http.StatusOK, content: true},
	"GET /api/v1/sources/{id}":         {target: "/api/v1/sources/1", status: http.StatusOK, content: true},
	"GET /api/v1/source-adds/{job_id}": {target: "/api/v1/source-adds/1", status: http.StatusOK, content: true},
	"GET /api/v1/jobs":                 {target: "/api/v1/jobs?state=failed&kind=source_add", status: http.StatusOK, content: true},
	"GET /api/v1/jobs/{id}":            {target: "/api/v1/jobs/2", status: http.StatusOK, content: true},
	"GET /api/v1/status":               {target: "/api/v1/status", status: http.StatusOK, content: true},
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
func assertNoSentinel(t *testing.T, w *httptest.ResponseRecorder, logs *syncBuffer) {
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
	for name, r := range map[string]*chi.Mux{"public": a.public, "admin": a.admin} {
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
				w := do(r, httptest.NewRequest(method, s.target, nil))
				assert.Equal(t, s.status, w.Code)
				assert.Equal(t, "hashed", w.Header().Get(headerFanwork))
				assertNoSentinel(t, w, logs)

				// Hostile input: sentinels in a param name and value, the request ID and the
				// body are never echoed or logged.
				sep := "?"
				if strings.Contains(s.target, "?") {
					sep = "&"
				}
				req := httptest.NewRequest(method, s.target+sep+sentinel+"name="+sentinel+"value",
					strings.NewReader(`{"x":"`+sentinel+`body"}`))
				req.Header.Set(headerRequestID, sentinel+" not a valid id")
				w = do(r, req)
				assert.Equal(t, http.StatusBadRequest, w.Code)
				assertNoSentinel(t, w, logs)

				if s.content {
					w = do(r, httptest.NewRequest(method, s.target+sep+RevealParam+"=1", nil))
					assert.Equal(t, s.status, w.Code)
					assert.Equal(t, "revealed", w.Header().Get(headerFanwork))
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
	a.public.Get("/leaktest", a.handle(func(*http.Request) (any, error) {
		return dto.Health{Status: sentinel + "status"}, nil
	}))

	w := get(a.public, "/leaktest")
	require.Equal(t, http.StatusOK, w.Code)
	assertNoSentinel(t, w, logs)
	var h dto.Health
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &h))
	assert.True(t, strings.HasPrefix(string(h.Status), "h_"), "hashed")
	assert.Contains(t, logs.String(), "enum value outside its set", "off-set enum value is warned about")

	w = get(a.public, "/leaktest"+revealQuery)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "revealed", w.Header().Get(headerFanwork))
	assert.Contains(t, w.Body.String(), sentinel+"status")
	assert.NotContains(t, logs.String(), sentinel, "revealed content is still never logged")
}

// TestEnumScopedToField checks that an enum field allows only its own type's values: every
// other DTO enum value (a WorkStatus, a JobKind, ...) in dto.Health.Status is hashed and
// warned about, while each type's own values pass in their own fields.
func TestEnumScopedToField(t *testing.T) {
	a, logs := newTestAPI(t, nil)
	var value string
	a.public.Get("/enum", a.handle(func(*http.Request) (any, error) {
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
		w := get(a.public, "/enum")
		require.Equal(t, http.StatusOK, w.Code)
		var h dto.Health
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &h))
		assert.True(t, strings.HasPrefix(string(h.Status), "h_"), "%q hashed in dto.Health.Status", v)
	}
	assert.Contains(t, logs.String(), "dto.Health.Status", "off-set enum warning names the field")

	value = string(dto.HealthOK)
	assert.JSONEq(t, `{"status":"ok"}`, get(a.public, "/enum").Body.String())
}

// openAPISpec is the part of docs/api/openapi.yaml the coverage test reads.
type openAPISpec struct {
	Paths map[string]map[string]yaml.Node `yaml:"paths"`
}

var openAPIMethods = map[string]string{
	"get": http.MethodGet, "put": http.MethodPut, "post": http.MethodPost, "delete": http.MethodDelete,
	"patch": http.MethodPatch, "query": methodQuery,
}

// TestRoutesMatchOpenAPI checks docs/api/openapi.yaml against the routers: the public router has
// exactly the operations not marked x-admin-only, and the admin router has all of them.
func TestRoutesMatchOpenAPI(t *testing.T) {
	raw, err := os.ReadFile("../../docs/api/openapi.yaml")
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
	assert.Equal(t, public, walkRoutes(t, a.public), "public routes vs openapi.yaml")
	assert.Equal(t, all, walkRoutes(t, a.admin), "admin routes vs openapi.yaml")
}
