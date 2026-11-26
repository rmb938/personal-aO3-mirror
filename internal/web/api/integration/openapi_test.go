package integration

import (
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

const openAPIPath = "../../../../docs/api/openapi.yaml"

// openAPISpec is the part of docs/api/openapi.yaml the tests read.
type openAPISpec struct {
	Paths    map[string]map[string]yaml.Node `yaml:"paths"`
	Security []map[string][]string           `yaml:"security"`
	// CommonHeaders (x-common-headers) are the headers any response can carry; responses
	// declare only their own.
	CommonHeaders map[string]specHeader `yaml:"x-common-headers"`
	Components    struct {
		Responses map[string]specResponse `yaml:"responses"`
		Headers   map[string]specHeader   `yaml:"headers"`
		Schemas   map[string]specSchema   `yaml:"schemas"`
	} `yaml:"components"`
}

// specSchema is the part of a schema TestOpenAPIVerbatimPatterns reads.
type specSchema struct {
	Ref        string                `yaml:"$ref"`
	AllOf      []specSchema          `yaml:"allOf"`
	Pattern    string                `yaml:"pattern"`
	Items      *specSchema           `yaml:"items"`
	Properties map[string]specSchema `yaml:"properties"`
}

// schema resolves s: a $ref to its component, and a single allOf (a $ref with its own
// description) to its member, unless s sets the pattern itself.
func (s *openAPISpec) schema(sc specSchema) specSchema {
	for range 8 { // refs don't nest deeper
		switch {
		case sc.Pattern != "":
			return sc
		case sc.Ref != "":
			sc = s.Components.Schemas[strings.TrimPrefix(sc.Ref, "#/components/schemas/")]
		case len(sc.AllOf) == 1:
			sc = sc.AllOf[0]
		default:
			return sc
		}
	}
	return sc
}

// specOp is one operation. Problems (x-problems) are the problem types it returns besides the
// ones every operation can (the Problem response's x-problems).
type specOp struct {
	AdminOnly bool `yaml:"x-admin-only"`
	// Security is the operation's own security, nil when it inherits the global one.
	Security  *[]map[string][]string  `yaml:"security"`
	Problems  []string                `yaml:"x-problems"`
	Responses map[string]specResponse `yaml:"responses"`
}

type specResponse struct {
	Ref      string                `yaml:"$ref"`
	Headers  map[string]specHeader `yaml:"headers"`
	Content  map[string]yaml.Node  `yaml:"content"`
	Problems []string              `yaml:"x-problems"`
}

type specHeader struct {
	Ref      string `yaml:"$ref"`
	Required bool   `yaml:"required"`
	Schema   struct {
		Enum []string `yaml:"enum"`
	} `yaml:"schema"`
}

var openAPIMethods = map[string]string{
	"get": http.MethodGet, "put": http.MethodPut, "post": http.MethodPost, "delete": http.MethodDelete,
	"patch": http.MethodPatch, "query": runtime.MethodQuery,
}

func loadOpenAPI() (*openAPISpec, error) {
	raw, err := os.ReadFile(openAPIPath)
	if err != nil {
		return nil, err
	}
	var spec openAPISpec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return nil, err
	}
	return &spec, nil
}

// ops returns every operation, keyed "METHOD path".
func (s *openAPISpec) ops() (map[string]specOp, error) {
	out := map[string]specOp{}
	for path, item := range s.Paths {
		for key, node := range item {
			method, ok := openAPIMethods[key]
			if !ok {
				continue
			}
			var op specOp
			if err := node.Decode(&op); err != nil {
				return nil, fmt.Errorf("%s %s: %w", key, path, err)
			}
			out[method+" "+path] = op
		}
	}
	return out, nil
}

func (s *openAPISpec) response(r specResponse) specResponse {
	if name, ok := strings.CutPrefix(r.Ref, "#/components/responses/"); ok {
		return s.Components.Responses[name]
	}
	return r
}

// headers returns the headers r can carry (the common ones and its own) by canonical name,
// with $refs resolved.
func (s *openAPISpec) headers(r specResponse) map[string]specHeader {
	out := map[string]specHeader{}
	for _, m := range []map[string]specHeader{s.CommonHeaders, s.response(r).Headers} {
		for name, h := range m {
			if ref, ok := strings.CutPrefix(h.Ref, "#/components/headers/"); ok {
				h = s.Components.Headers[ref]
			}
			out[http.CanonicalHeaderKey(name)] = h
		}
	}
	return out
}

// TestRoutesMatchOpenAPI checks docs/api/openapi.yaml against the routers: the public router has
// exactly the operations not marked x-admin-only, and the admin router has all of them.
func TestRoutesMatchOpenAPI(t *testing.T) {
	spec, err := loadOpenAPI()
	require.NoError(t, err)
	ops, err := spec.ops()
	require.NoError(t, err)
	var public, all []string
	for rt, op := range ops {
		all = append(all, rt)
		if !op.AdminOnly {
			public = append(public, rt)
		}
	}
	slices.Sort(public)
	slices.Sort(all)
	require.NotEmpty(t, public)

	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	assert.Equal(t, public, walkRoutes(t, a.Router(false)), "public routes vs openapi.yaml")
	assert.Equal(t, all, walkRoutes(t, a.Router(true)), "admin routes vs openapi.yaml")
}

// TestOpenAPISecurity: the spec requires bearerAuth globally, and exactly the operations the
// public listener serves without a token (route.Anonymous) or doesn't serve (x-admin-only)
// opt out with `security: []`. TestPublicRoutesNeedToken checks the server side.
func TestOpenAPISecurity(t *testing.T) {
	spec, err := loadOpenAPI()
	require.NoError(t, err)
	assert.Equal(t, []map[string][]string{{"bearerAuth": {}}}, spec.Security, "global security")
	ops, err := spec.ops()
	require.NoError(t, err)
	for rt, op := range ops {
		_, pattern, _ := strings.Cut(rt, " ")
		if route.Anonymous(pattern) || op.AdminOnly {
			require.NotNil(t, op.Security, "%s: needs no token, so security: []", rt)
			assert.Empty(t, *op.Security, "%s: needs no token, so security: []", rt)
		} else {
			assert.Nil(t, op.Security, "%s: needs a token, so inherits the global security", rt)
		}
	}
}

// TestOpenAPIProblemTypes: every x-problems entry is a problem type, and the Problem response
// (the default of every operation) lists the types any operation can return.
func TestOpenAPIProblemTypes(t *testing.T) {
	spec, err := loadOpenAPI()
	require.NoError(t, err)
	ops, err := spec.ops()
	require.NoError(t, err)
	known := map[string]bool{}
	for _, v := range dto.ProblemType("").EnumValues() {
		known[strings.TrimPrefix(v, dto.ProblemTypeBase)] = true
	}
	for name, h := range spec.CommonHeaders {
		ref, ok := strings.CutPrefix(h.Ref, "#/components/headers/")
		assert.True(t, ok, "x-common-headers %s: not a components.headers $ref", name)
		assert.Contains(t, spec.Components.Headers, ref, "x-common-headers %s", name)
	}
	common := spec.Components.Responses["Problem"].Problems
	require.NotEmpty(t, common, "components.responses.Problem has x-problems")
	for _, slug := range common {
		assert.True(t, known[slug], "Problem x-problems: %q is not a problem type", slug)
	}
	for rt, op := range ops {
		assert.Contains(t, op.Responses, "default", "%s: no default (problem) response", rt)
		for _, slug := range op.Problems {
			assert.True(t, known[slug], "%s x-problems: %q is not a problem type", rt, slug)
			assert.NotContains(t, common, slug, "%s x-problems: %q is already common to every operation", rt, slug)
		}
	}
}

// TestOpenAPIVerbatimPatterns: each dto Verbatim field has, in the spec schema named after its
// type, its VerbatimPattern anchored (^…$), so a generated client accepts exactly what the
// enforcer passes. (Verbatim fields are only in the auth DTOs listed here.)
func TestOpenAPIVerbatimPatterns(t *testing.T) {
	spec, err := loadOpenAPI()
	require.NoError(t, err)
	checked := map[reflect.Type]bool{}
	for _, v := range []any{
		dto.Enrollment{}, dto.RecoveryCodes{}, dto.LoginChallenge{}, dto.TokenPair{},
		dto.InviteLink{}, dto.User{}, dto.Account{},
	} {
		typ := reflect.TypeOf(v)
		schema, ok := spec.Components.Schemas[typ.Name()]
		require.True(t, ok, "components.schemas.%s", typ.Name())
		for sf := range typ.Fields() {
			ft := sf.Type
			if ft.Kind() == reflect.Slice {
				ft = ft.Elem()
			}
			vb, ok := reflect.TypeAssert[interface{ VerbatimPattern() string }](reflect.Zero(ft))
			if !ok {
				continue
			}
			checked[ft] = true
			key, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
			prop, ok := schema.Properties[key]
			require.True(t, ok, "%s.%s: not in the spec", typ.Name(), key)
			prop = spec.schema(prop)
			if sf.Type.Kind() == reflect.Slice {
				require.NotNil(t, prop.Items, "%s.%s: not an array", typ.Name(), key)
				prop = spec.schema(*prop.Items)
			}
			assert.Equal(t, "^"+vb.VerbatimPattern()+"$", prop.Pattern, "%s.%s (%s)", typ.Name(), key, ft.Name())
		}
	}
	assert.Len(t, checked, 9, "every dto Verbatim type is checked") // dto/verbatim.go
}

// TestRequestProblems sends every route the requests any route of its kind refuses before the
// handler: a cross-origin browser request to an unsafe method (403 forbidden), and to a route
// taking a body, one without the JSON media type (415) and one over the size cap (413).
//
// A route needing an access token is sent the body checks on the public listener with one (the
// admin socket refuses it with 401 before reading the body).
func TestRequestProblems(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	token := tokenFor(t, a)
	refused := func(t *testing.T, req *http.Request, status int, typ dto.ProblemType, msg string) *httptest.ResponseRecorder {
		t.Helper()
		h := a.Admin()
		if strings.HasPrefix(req.Header.Get("Authorization"), "Bearer ") {
			h = a.Public()
		}
		w := testutil.Do(t, h, req)
		require.Equal(t, status, w.Code, msg)
		assert.Equal(t, typ, testutil.DecodeProblem(t, w).Type, msg)
		return w
	}
	for rt, s := range routeSamples {
		method, _, _ := strings.Cut(rt, " ")
		if method == http.MethodGet {
			continue
		}
		t.Run(rt, func(t *testing.T) {
			req := s.request(method, "")
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			refused(t, req, http.StatusForbidden, dto.TypeForbidden, "cross-origin")
			if s.body == "" {
				return
			}
			authed := func(req *http.Request) *http.Request {
				if s.account {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				return req
			}
			req = authed(s.request(method, ""))
			req.Header.Set("Content-Type", "text/plain")
			w := refused(t, req, http.StatusUnsupportedMediaType, dto.TypeUnsupportedMediaType, "media type")
			if method == runtime.MethodQuery {
				assert.Equal(t, runtime.ContentTypeJSON, w.Header().Get(runtime.HeaderAcceptQuery))
			}
			req = authed(httptest.NewRequest(method, s.target,
				strings.NewReader(`{"x":"`+strings.Repeat("a", runtime.MaxBodyBytes)+`"}`)))
			req.Header.Set("Content-Type", runtime.ContentTypeJSON)
			refused(t, req, http.StatusRequestEntityTooLarge, dto.TypeBodyTooLarge, "too large")
		})
	}
}

// standardHeaders are response headers the spec does not declare: entity headers and the
// security headers every response carries (docs/part2-api.md, "HTTP hardening"), which are for
// browsers, not API clients. Cache-Control is declared (x-common-headers): no-store is part of
// the token responses' contract (RFC 6749 §5.1).
var standardHeaders = []string{
	"Content-Type", "Content-Length", "Content-Security-Policy", "X-Content-Type-Options",
	"Referrer-Policy", "Cross-Origin-Resource-Policy",
}

// statusHeaders are headers every response with the status must carry, whatever the operation
// declares: a 401 names the scheme (RFC 9110 §15.5.2) and a 429 says when to retry (RFC 6585
// §4; every limit, the login backoff included, knows it).
var statusHeaders = map[int]string{
	http.StatusUnauthorized:    "WWW-Authenticate",
	http.StatusTooManyRequests: "Retry-After",
}

// specChecker checks every response the integration tests see against docs/api/openapi.yaml
// (TestMain installs it as the testutil observer). Each response must match its operation: a
// declared status, a declared content type, only declared headers, every required one and the
// statusHeaders, a value from a header's enum when it has one, and for a problem a type the
// operation lists. It also records what it saw, so a full run can
// report the statuses and problem types the spec lists but no test produced.
type specChecker struct {
	spec   *openAPISpec
	ops    map[string]specOp
	common []string
	// patterns are the operations' paths split into segments, for matching request paths.
	patterns [][]string

	mu       sync.Mutex
	seen     map[string]bool // "METHOD path"
	statuses map[string]map[string]bool
	problems map[string]map[string]bool
}

func newSpecChecker() (*specChecker, error) {
	spec, err := loadOpenAPI()
	if err != nil {
		return nil, err
	}
	ops, err := spec.ops()
	if err != nil {
		return nil, err
	}
	c := &specChecker{
		spec: spec, ops: ops, common: spec.Components.Responses["Problem"].Problems,
		seen: map[string]bool{}, statuses: map[string]map[string]bool{}, problems: map[string]map[string]bool{},
	}
	for _, path := range slices.Sorted(maps.Keys(spec.Paths)) {
		c.patterns = append(c.patterns, strings.Split(path, "/"))
	}
	return c, nil
}

// match returns the spec path a request path is routed to, or "". Like chi, a literal segment
// wins over a parameter at the first position where two candidates differ.
func (c *specChecker) match(escapedPath string) string {
	segs := strings.Split(escapedPath, "/")
	var best []string
	for _, p := range c.patterns {
		if len(p) != len(segs) || !segmentsMatch(p, segs) {
			continue
		}
		if best == nil || moreLiteral(p, best) {
			best = p
		}
	}
	return strings.Join(best, "/")
}

func isParam(seg string) bool { return strings.HasPrefix(seg, "{") }

func segmentsMatch(pattern, segs []string) bool {
	for i, p := range pattern {
		if isParam(p) && segs[i] == "" || !isParam(p) && p != segs[i] {
			return false
		}
	}
	return true
}

func moreLiteral(a, b []string) bool {
	for i := range a {
		if pa, pb := isParam(a[i]), isParam(b[i]); pa != pb {
			return pb
		}
	}
	return false
}

// observe is the testutil.Observer.
func (c *specChecker) observe(tb testing.TB, req *http.Request, w *httptest.ResponseRecorder) {
	tb.Helper()
	path := c.match(req.URL.EscapedPath())
	rt := req.Method + " " + path
	op, ok := c.ops[rt]
	if path == "" || !ok {
		return // no route (404) or not this method (405): no operation to check
	}
	status := strconv.Itoa(w.Code)
	slug := ""
	resp, declared := op.Responses[status]
	if !declared {
		resp = op.Responses["default"]
	}
	ct, _, _ := mime.ParseMediaType(w.Header().Get("Content-Type"))
	if w.Code >= 400 {
		var p dto.Problem
		if ct != runtime.ContentTypeProblem || json.Unmarshal(w.Body.Bytes(), &p) != nil {
			tb.Errorf("openapi: %s: %d is not a problem", rt, w.Code)
			return
		}
		slug = strings.TrimPrefix(string(p.Type), dto.ProblemTypeBase)
		if !slices.Contains(c.common, slug) && !slices.Contains(op.Problems, slug) {
			tb.Errorf("openapi: %s: problem type %q is not in its x-problems", rt, slug)
		}
	} else if !declared {
		tb.Errorf("openapi: %s: status %d is not declared", rt, w.Code)
	}
	c.checkContent(tb, rt, status, ct, c.spec.response(resp))
	c.checkHeaders(tb, rt, status, w.Header(), c.spec.headers(resp))
	if name, ok := statusHeaders[w.Code]; ok && w.Header().Get(name) == "" {
		tb.Errorf("openapi: %s %s: header %s is missing", rt, status, name)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen[rt] = true
	if c.statuses[rt] == nil {
		c.statuses[rt], c.problems[rt] = map[string]bool{}, map[string]bool{}
	}
	c.statuses[rt][status] = true
	if slug != "" {
		c.problems[rt][slug] = true
	}
}

func (c *specChecker) checkContent(tb testing.TB, rt, status, ct string, resp specResponse) {
	tb.Helper()
	if ct == "" {
		if len(resp.Content) > 0 {
			tb.Errorf("openapi: %s %s: no body, but the spec declares content", rt, status)
		}
		return
	}
	for declared := range resp.Content {
		if declared == ct || strings.HasSuffix(declared, "/*") && strings.HasPrefix(ct, strings.TrimSuffix(declared, "*")) {
			return
		}
	}
	tb.Errorf("openapi: %s %s: content type %q is not declared", rt, status, ct)
}

func (c *specChecker) checkHeaders(tb testing.TB, rt, status string, got http.Header, declared map[string]specHeader) {
	tb.Helper()
	for name, vals := range got {
		h, ok := declared[name]
		if !ok && !slices.Contains(standardHeaders, name) {
			tb.Errorf("openapi: %s %s: header %s is not declared", rt, status, name)
		}
		for _, v := range vals {
			if len(h.Schema.Enum) > 0 && !slices.Contains(h.Schema.Enum, v) {
				tb.Errorf("openapi: %s %s: header %s: %q is not in its enum", rt, status, name, v)
			}
		}
	}
	for name, h := range declared {
		if h.Required && got.Get(name) == "" {
			tb.Errorf("openapi: %s %s: required header %s is missing", rt, status, name)
		}
	}
}

// gaps lists what the spec declares that no test produced: an operation, a declared status or
// one of an operation's own x-problems. The common problem types come from the middleware,
// whose own tests (runtime) cover them.
func (c *specChecker) gaps() []string {
	var out []string
	for _, rt := range slices.Sorted(maps.Keys(c.ops)) {
		op := c.ops[rt]
		if !c.seen[rt] {
			out = append(out, rt+": never requested")
			continue
		}
		for status := range op.Responses {
			if status != "default" && !c.statuses[rt][status] {
				out = append(out, rt+": no test got status "+status)
			}
		}
		for _, slug := range op.Problems {
			if !c.problems[rt][slug] {
				out = append(out, rt+": no test got problem "+slug)
			}
		}
	}
	slices.Sort(out)
	return out
}

// fullRun reports whether every test in the package runs (no -run, -skip or fuzzing), so the
// gaps are real.
func fullRun() bool {
	for _, name := range []string{"test.run", "test.skip", "test.fuzz"} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" {
			return false
		}
	}
	return true
}

// TestMain checks every response the package's tests see against docs/api/openapi.yaml, and on a
// full run fails when a declared status or problem type was never produced (the spec drifted
// from the server, or the tests miss a case).
func TestMain(m *testing.M) {
	flag.Parse()
	c, err := newSpecChecker()
	if err != nil {
		fmt.Fprintln(os.Stderr, "openapi:", err)
		os.Exit(1)
	}
	testutil.Observe(c.observe)
	code := m.Run()
	testutil.Observe(nil)
	if code == 0 && fullRun() {
		if gaps := c.gaps(); len(gaps) > 0 {
			fmt.Fprintln(os.Stderr, "openapi: declared but never produced by a test:")
			for _, g := range gaps {
				fmt.Fprintln(os.Stderr, "  "+g)
			}
			code = 1
		}
	}
	os.Exit(code)
}
