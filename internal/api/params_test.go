package api

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

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
)

// requireAPIError asserts err is an *Error of type typ and, when params are given, a validation
// error naming exactly those (raw) parameters in order.
func requireAPIError(t *testing.T, err error, typ dto.ProblemType, params ...string) {
	t.Helper()
	e := asAPIError(t, err)
	assert.Equal(t, typ, e.Type)
	got := make([]string, len(e.Errors))
	for i, item := range e.Errors {
		got[i] = item.Param
		assert.NotEmpty(t, item.Detail.msg, "every error item explains itself")
	}
	if len(params) == 0 {
		params = []string{}
	}
	assert.Equal(t, params, got)
}

// asAPIError requires err to be an *Error and returns it.
func asAPIError(t *testing.T, err error) *Error {
	t.Helper()
	e, ok := errors.AsType[*Error](err)
	require.True(t, ok, "want *Error, got %v", err)
	return e
}

// testParams parses raw as a request's query with the given allowed names, and chi path
// params (name, value pairs).
func testParams(t *testing.T, raw string, path []string, allowed ...string) (*params, error) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/x?"+raw, nil)
	if len(path) > 0 {
		rctx := chi.NewRouteContext()
		for i := 0; i+1 < len(path); i += 2 {
			rctx.URLParams.Add(path[i], path[i+1])
		}
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	}
	return parseParams(r, allowed...)
}

func TestParseParams(t *testing.T) {
	p, err := testParams(t, "page=2&per_page=10", nil, "page", "per_page")
	require.NoError(t, err)
	require.NoError(t, p.err())
	assert.Equal(t, map[string]string{"page": "2", "per_page": "10"}, p.vals)

	p, err = testParams(t, "", nil)
	require.NoError(t, err)
	require.NoError(t, p.err())

	// The reveal param is allowed everywhere but not handed to the handler.
	p, err = testParams(t, RevealParam+"=1&page=1", nil, "page")
	require.NoError(t, err)
	require.NoError(t, p.err())
	assert.Equal(t, map[string]string{"page": "1"}, p.vals)

	// Every unknown or repeated name is reported, in name order; the valid ones still parse.
	p, err = testParams(t, "zz=1&SENTINEL=1&page=1&page=2&per_page=5&"+RevealParam+"=1&"+RevealParam+"=1", nil,
		"page", "per_page")
	require.NoError(t, err)
	requireAPIError(t, p.err(), dto.TypeValidation, "SENTINEL", "page", RevealParam, "zz")
	assert.Equal(t, map[string]string{"per_page": "5"}, p.vals)
	// An undeclared name is never echoed; a declared one the route doesn't take is named.
	prob := asAPIError(t, p.err()).problem()
	assert.Empty(t, prob.Errors[0].Parameter)
	assert.Equal(t, dto.ParamPage, prob.Errors[1].Parameter)

	for _, raw := range []string{"page=%zz", "page=1;per_page=2", strings.Repeat("a=1&", maxQueryParams) + "a=1"} {
		_, err = testParams(t, raw, nil, "page", "per_page", "a")
		requireAPIError(t, err, dto.TypeBadRequest)
	}
}

func TestParseInt(t *testing.T) {
	ok := map[string]int64{"1": 1, "42": 42, "007": 7, "100": 100}
	for s, want := range ok {
		v, valid := parseInt(s, 100)
		require.True(t, valid, s)
		assert.Equal(t, want, v, s)
	}
	for _, s := range []string{"", "0", "101", "+5", "-1", "1.5", "1e3", " 1", "1 ", "0x10", "abc",
		"99999999999999999999"} {
		_, valid := parseInt(s, 100)
		assert.False(t, valid, s)
	}
}

func TestParamGetters(t *testing.T) {
	p, err := testParams(t, "state=bogus&kind=work&source_id=0&n=7", []string{"id", "9007199254740991", "job_id", "x"},
		"state", "kind", "source_id", "n")
	require.NoError(t, err)
	assert.Empty(t, p.enum("state", dto.JobState(""), detailJobState))
	assert.Equal(t, "work", p.enum("kind", dto.JobKind(""), detailJobKind))
	assert.Empty(t, p.enum("absent", dto.JobKind(""), detailJobKind))
	assert.Zero(t, p.int("source_id", 0, MaxID, detailID))
	assert.Equal(t, int64(7), p.int("n", 0, 10, detailID))
	assert.Equal(t, int64(3), p.int("absent", 3, 10, detailID), "absent: the default")
	assert.Equal(t, int64(MaxID), p.pathID(dto.ParamID))
	assert.Zero(t, p.pathID(dto.ParamJobID))
	requireAPIError(t, p.err(), dto.TypeValidation, "state", "source_id", "job_id")
	e := asAPIError(t, p.err())
	assert.Equal(t, []detail{detailJobState, detailID, detailID}, []detail{e.Errors[0].Detail, e.Errors[1].Detail, e.Errors[2].Detail})
}

func TestPage(t *testing.T) {
	cases := []struct {
		raw        string
		page, per  int64
		bad        []string
		wantOffset int64
	}{
		{raw: "", page: 1, per: defaultPerPage},
		{raw: "page=3&per_page=50", page: 3, per: 50, wantOffset: 100},
		{raw: "page=1001&per_page=100", page: 1001, per: 100, wantOffset: maxOffset},
		{raw: "page=1002&per_page=100", bad: []string{"page"}},
		{raw: "page=100001&per_page=1", page: 100001, per: 1, wantOffset: maxOffset},
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
		pg := p.page()
		if len(c.bad) > 0 {
			requireAPIError(t, p.err(), dto.TypeValidation, c.bad...)
			continue
		}
		require.NoError(t, p.err(), c.raw)
		assert.Equal(t, Page{Page: c.page, PerPage: c.per}, pg)
		assert.Equal(t, c.wantOffset, pg.Offset())
		assert.LessOrEqual(t, pg.Offset(), int64(maxOffset))
	}
}

func TestValidText(t *testing.T) {
	assert.True(t, validText("hello wörld", 11))
	assert.True(t, validText("", 1))
	for _, s := range []string{"\x00", "a\nb", "tab\t", "\u0085", "bad\xff", "twelve runes"} {
		assert.False(t, validText(s, 11), "%q", s)
	}
}
