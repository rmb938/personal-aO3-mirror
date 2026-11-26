package runtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
)

// requireAPIError asserts err is a *problem.Error of type typ and, when params are given, a
// validation error naming exactly those (raw) parameters in order.
func requireAPIError(t *testing.T, err error, typ dto.ProblemType, params ...string) {
	t.Helper()
	e := asAPIError(t, err)
	assert.Equal(t, typ, e.Type)
	got := make([]string, len(e.Errors))
	for i, item := range e.Errors {
		got[i] = item.Param
		assert.NotEmpty(t, item.Detail.Message(), "every error item explains itself")
	}
	if len(params) == 0 {
		params = []string{}
	}
	assert.Equal(t, params, got)
}

// asAPIError requires err to be a *problem.Error and returns it.
func asAPIError(t *testing.T, err error) *problem.Error {
	t.Helper()
	e, ok := errors.AsType[*problem.Error](err)
	require.True(t, ok, "want *problem.Error, got %v", err)
	return e
}

// testParams parses raw as a request's query with the given allowed names, and chi path
// params (name, value pairs).
func testParams(t *testing.T, raw string, path []string, allowed ...string) (*Params, error) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/x?"+raw, nil)
	if len(path) > 0 {
		rctx := chi.NewRouteContext()
		for i := 0; i+1 < len(path); i += 2 {
			rctx.URLParams.Add(path[i], path[i+1])
		}
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	}
	return ParseParams(r, allowed...)
}

func TestParseParams(t *testing.T) {
	p, err := testParams(t, "page=2&per_page=10", nil, "page", "per_page")
	require.NoError(t, err)
	require.NoError(t, p.Err())
	assert.Equal(t, map[string]string{"page": "2", "per_page": "10"}, p.Values())

	p, err = testParams(t, "", nil)
	require.NoError(t, err)
	require.NoError(t, p.Err())

	// The reveal param is allowed everywhere but not handed to the handler.
	p, err = testParams(t, RevealParam+"=1&page=1", nil, "page")
	require.NoError(t, err)
	require.NoError(t, p.Err())
	assert.Equal(t, map[string]string{"page": "1"}, p.Values())

	// Every unknown or repeated name is reported, in name order; the valid ones still parse.
	p, err = testParams(t, "zz=1&SENTINEL=1&page=1&page=2&per_page=5&"+RevealParam+"=1&"+RevealParam+"=1", nil,
		"page", "per_page")
	require.NoError(t, err)
	requireAPIError(t, p.Err(), dto.TypeValidation, "SENTINEL", "page", RevealParam, "zz")
	assert.Equal(t, map[string]string{"per_page": "5"}, p.Values())
	// An undeclared name is never echoed; a declared one the route doesn't take is named.
	prob := asAPIError(t, p.Err()).Problem()
	assert.Empty(t, prob.Errors[0].Parameter)
	assert.Equal(t, dto.ParamPage, prob.Errors[1].Parameter)

	for _, raw := range []string{"page=%zz", "page=1;per_page=2", strings.Repeat("a=1&", MaxQueryParams) + "a=1"} {
		_, err = testParams(t, raw, nil, "page", "per_page", "a")
		requireAPIError(t, err, dto.TypeBadRequest)
	}
}

func TestParseInt(t *testing.T) {
	ok := map[string]int64{"1": 1, "42": 42, "007": 7, "100": 100}
	for s, want := range ok {
		v, valid := ParseInt(s, 100)
		require.True(t, valid, s)
		assert.Equal(t, want, v, s)
	}
	for _, s := range []string{"", "0", "101", "+5", "-1", "1.5", "1e3", " 1", "1 ", "0x10", "abc",
		"99999999999999999999"} {
		_, valid := ParseInt(s, 100)
		assert.False(t, valid, s)
		_, valid = ParseSmallInt(s, 100)
		assert.False(t, valid, s)
	}
	for s, want := range ok {
		v, valid := ParseSmallInt(s, 100)
		require.True(t, valid, s)
		assert.Equal(t, int(want), v, s)
	}
}

func TestParamGetters(t *testing.T) {
	p, err := testParams(t, "state=bogus&kind=work&source_id=0&n=7", []string{"id", "9007199254740991", "job_id", "x"},
		"state", "kind", "source_id", "n")
	require.NoError(t, err)
	assert.Empty(t, p.Enum("state", dto.JobState(""), DetailJobState))
	assert.Equal(t, "work", p.Enum("kind", dto.JobKind(""), DetailJobKind))
	assert.Empty(t, p.Enum("absent", dto.JobKind(""), DetailJobKind))
	assert.Zero(t, p.Int("source_id", 0, MaxID, DetailID))
	assert.Equal(t, int64(7), p.Int("n", 0, 10, DetailID))
	assert.Equal(t, int64(3), p.Int("absent", 3, 10, DetailID), "absent: the default")
	assert.Equal(t, int64(MaxID), p.PathID(dto.ParamID))
	assert.Zero(t, p.PathID(dto.ParamJobID))
	requireAPIError(t, p.Err(), dto.TypeValidation, "state", "source_id", "job_id")
	e := asAPIError(t, p.Err())
	assert.Equal(t, []problem.Detail{DetailJobState, DetailID, DetailID},
		[]problem.Detail{e.Errors[0].Detail, e.Errors[1].Detail, e.Errors[2].Detail})
}

func TestPage(t *testing.T) {
	cases := []struct {
		raw        string
		page, per  int
		bad        []string
		wantOffset int
	}{
		{raw: "", page: 1, per: DefaultPerPage},
		{raw: "page=3&per_page=50", page: 3, per: 50, wantOffset: 100},
		{raw: "page=1001&per_page=100", page: 1001, per: 100, wantOffset: MaxOffset},
		{raw: "page=1002&per_page=100", bad: []string{"page"}},
		{raw: "page=100001&per_page=1", page: 100001, per: 1, wantOffset: MaxOffset},
		{raw: "page=100002&per_page=1", bad: []string{"page"}},
		{raw: "page=9223372036854775807", bad: []string{"page"}},
		{raw: "page=0", bad: []string{"page"}},
		{raw: "per_page=0", bad: []string{"per_page"}},
		{raw: "per_page=101", bad: []string{"per_page"}},
		{raw: "page=0&per_page=0", bad: []string{"per_page", "page"}},
		{raw: "page=5000&per_page=0", bad: []string{"per_page"}, page: 5000}, // no cap check on a bad per_page
	}
	for _, c := range cases {
		p, err := testParams(t, c.raw, nil, "page", "per_page")
		require.NoError(t, err)
		pg := p.Page()
		if len(c.bad) > 0 {
			requireAPIError(t, p.Err(), dto.TypeValidation, c.bad...)
			continue
		}
		require.NoError(t, p.Err(), c.raw)
		assert.Equal(t, Page{Page: c.page, PerPage: c.per}, pg)
		assert.Equal(t, c.wantOffset, pg.Offset())
		assert.LessOrEqual(t, pg.Offset(), MaxOffset)
	}
}

func TestPathTagName(t *testing.T) {
	// Through a chi router, so the value is escaped exactly when chi routes on RawPath.
	get := func(target string) (string, error) {
		var name string
		var err error
		r := chi.NewRouter()
		r.Get("/t/{name}", func(_ http.ResponseWriter, r *http.Request) {
			p, perr := ParseParams(r)
			if perr != nil {
				err = perr
				return
			}
			name = p.PathTagName(dto.ParamTagName)
			err = p.Err()
		})
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))
		return name, err
	}
	for target, want := range map[string]string{
		"/t/Fixture%20Tag":                           "Fixture Tag",
		"/t/Fixture*s*Tag*d*One":                     "Fixture/Tag.One",
		"/t/A*a*B*q*C*h*D":                           "A&B?C#D",
		"/t/Fixture%2FTag":                           "Fixture/Tag",   // escaped slash: RawPath is set
		"/t/Fixture%2FTag%20%C3%A9":                  "Fixture/Tag é", // and every other escape is decoded with it
		"/t/" + strings.Repeat("a", MaxTagNameRunes): strings.Repeat("a", MaxTagNameRunes),
	} {
		got, err := get(target)
		require.NoError(t, err, target)
		assert.Equal(t, want, got, target)
	}
	for _, target := range []string{
		"/t/a%01b", "/t/a%2Fb%7F", "/t/" + strings.Repeat("a", MaxTagNameRunes+1), "/t/%FF",
	} {
		got, err := get(target)
		requireAPIError(t, err, dto.TypeValidation, string(dto.ParamTagName))
		assert.Empty(t, got, target)
	}
}
