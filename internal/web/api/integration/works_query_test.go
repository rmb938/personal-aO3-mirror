package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

// queryJSON sends a QUERY to target with body as application/json.
func queryJSON(t *testing.T, h http.Handler, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(runtime.MethodQuery, target, strings.NewReader(body))
	req.Header.Set("Content-Type", runtime.ContentTypeJSON)
	return testutil.Do(t, h, req)
}

// queryWorks sends a work query, requires 200 and decodes the page.
func queryWorks(t *testing.T, h http.Handler, target, body string) dto.List[dto.WorkBlurb] {
	t.Helper()
	w := queryJSON(t, h, target, body)
	require.Equal(t, http.StatusOK, w.Code, body+": "+w.Body.String())
	assert.Equal(t, runtime.ContentTypeJSON, w.Header().Get(runtime.HeaderAcceptQuery), "QUERY advertised")
	var l dto.List[dto.WorkBlurb]
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &l))
	return l
}

func TestQueryWorks(t *testing.T) {
	require.Equal(t, int64(runtime.MaxID), int64(request.MaxID), "body ids have the query's range")

	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()

	g := testutil.Get(t, h, "/api/v1/works")
	assert.Equal(t, runtime.ContentTypeJSON, g.Header().Get(runtime.HeaderAcceptQuery), "GET advertises QUERY")

	l := queryWorks(t, h, "/api/v1/works", `{}`)
	assert.Equal(t, 2, l.Total)
	assert.Equal(t, []int64{seedFetchedWork, seedWork}, workIDs(t, l), "{} is GET's default listing")
	assert.True(t, isHash(t, l.Items[0].Title), "blurb text hashed")

	// The fetched work's tags, by id (rating, fandom); the blurb-only work has a freeform.
	work := getJSON[dto.Work](t, h, "/api/v1/works/9")
	require.Len(t, work.Tags, 2)
	fandom := strconv.FormatInt(work.Tags[1].ID, 10)

	for body, want := range map[string][]int64{
		`{"fetched":true}`:                                    {seedFetchedWork},
		`{"fetched":false}`:                                   {seedWork},
		`{"words":{"min":2000}}`:                              {seedFetchedWork},
		`{"words":{"min":1200,"max":1200}}`:                   {seedWork},
		`{"kudos":{"max":4}}`:                                 {},
		`{"tag_ids":[` + fandom + `]}`:                        {seedFetchedWork},
		`{"exclude_tag_ids":[` + fandom + `]}`:                {seedWork},
		`{"statuses":["deleted","hidden"]}`:                   {},
		`{"statuses":["ok"],"complete":false}`:                {seedFetchedWork, seedWork},
		`{"complete":true}`:                                   {},
		`{"single_chapter":true}`:                             {},
		`{"published":{"from":"2025-01-01"}}`:                 {seedFetchedWork}, // blurb-only: no published date
		`{"revised":{"to":"2025-03-03"}}`:                     {},
		`{"revised":{"from":"2025-03-04","to":"2025-03-04"}}`: {seedFetchedWork, seedWork},
		`{"sort":"words","direction":"asc"}`:                  {seedWork, seedFetchedWork},
		`{"sort":"title"}`:                                    {seedWork, seedFetchedWork}, // ascending by default
		`{"crossover":true}`:                                  {},
	} {
		l := queryWorks(t, h, "/api/v1/works", body)
		assert.Equal(t, want, workIDs(t, l), body)
		assert.Equal(t, len(want), l.Total, body)
	}

	l = queryWorks(t, h, "/api/v1/works?page=2&per_page=1", `{"sort":"words"}`)
	assert.Equal(t, []int64{seedWork}, workIDs(t, l), "paging is in the query string")
	assert.Equal(t, 2, l.Total)
	assert.Equal(t, int64(2), l.Page)
	assert.Equal(t, int64(1), l.PerPage)

	// Every invalid parameter and member is listed in one response.
	bad := queryJSON(t, h, "/api/v1/works?per_page=0",
		`{"tag_ids":[0],"statuses":["gone"],"words":{"min":5,"max":1},"published":{"from":"x"},"sort":"y"}`)
	require.Equal(t, http.StatusUnprocessableEntity, bad.Code, bad.Body.String())
	var where []string
	for _, e := range testutil.DecodeProblem(t, bad).Errors {
		where = append(where, string(e.Parameter)+e.Pointer)
	}
	assert.Equal(t, []string{"per_page", "/tag_ids/0", "/statuses/0", "/words", "/published/from", "/sort"}, where,
		"pointers pass the enforcer (they name WorkQuery members)")

	for name, tc := range map[string]struct {
		target, body string
		typ          dto.ProblemType
	}{
		"unknown member":  {"/api/v1/works", `{"query":"x"}`, dto.TypeInvalidBody},
		"q hashed":        {"/api/v1/works", `{"q":"title"}`, dto.TypeRevealRequired},
		"q invalid":       {"/api/v1/works" + revealQuery, `{"q":"-title"}`, dto.TypeValidation},
		"best match no q": {"/api/v1/works" + revealQuery, `{"sort":"best_match"}`, dto.TypeValidation},
		"wrong kind":      {"/api/v1/works", `{"tag_ids":"1"}`, dto.TypeInvalidBody},
		"trailing data":   {"/api/v1/works", `{}{}`, dto.TypeInvalidBody},
		"sort in query":   {"/api/v1/works?sort=title", `{}`, dto.TypeValidation},
		"language hashed": {"/api/v1/works", `{"language":"Fixture Language"}`, dto.TypeRevealRequired},
	} {
		w := queryJSON(t, h, tc.target, tc.body)
		assert.Equal(t, tc.typ, testutil.DecodeProblem(t, w).Type, name)
	}
	w := queryJSON(t, h, "/api/v1/works", `{"language":"Fixture Language"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, runtime.DetailFreeText.Message(), testutil.DecodeProblem(t, w).Detail)

	// Without a JSON body: 415, advertising the accepted type.
	w = testutil.Do(t, h, httptest.NewRequest(runtime.MethodQuery, "/api/v1/works", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusUnsupportedMediaType, w.Code)
	assert.Equal(t, runtime.ContentTypeJSON, w.Header().Get(runtime.HeaderAcceptQuery))

	// Revealed: language filters, and the content is real.
	l = queryWorks(t, h, "/api/v1/works"+revealQuery, `{"language":"`+sentinel+`lang","fetched":true}`)
	assert.Equal(t, []int64{seedFetchedWork}, workIDs(t, l))
	assert.Equal(t, sentinel+"title", l.Items[0].Title)
	l = queryWorks(t, h, "/api/v1/works"+revealQuery, `{"language":"Fixture Language"}`)
	assert.Empty(t, l.Items)
}

// TestScopedQueryProblems: a QUERY scoped to a tag, creator or collection refuses free text in
// hashed mode and an unknown scope with 404.
func TestScopedQueryProblems(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	for pattern, detail := range map[string]problem.Detail{
		route.TagWorks:        runtime.DetailNoTag,
		route.CreatorWorks:    runtime.DetailNoCreator,
		route.CollectionWorks: runtime.DetailNoCollection,
	} {
		w := queryJSON(t, h, route.Path(pattern, 999), `{}`)
		require.Equal(t, http.StatusNotFound, w.Code, pattern)
		assert.Equal(t, detail.Message(), testutil.DecodeProblem(t, w).Detail, pattern)

		w = queryJSON(t, h, route.Path(pattern, 1), `{"q":"title"}`)
		require.Equal(t, http.StatusBadRequest, w.Code, pattern)
		assert.Equal(t, dto.TypeRevealRequired, testutil.DecodeProblem(t, w).Type, pattern)
	}
}

// TestQueryWorksSearch runs q (revealed) over the seeded works: both titles are
// SENTINEL-title, only seedFetchedWork is in a series and only seedWork has a freeform tag.
func TestQueryWorksSearch(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	for body, want := range map[string][]int64{
		`{"q":"series"}`:                                   {seedFetchedWork},
		`{"q":"tag"}`:                                      {seedWork},
		`{"q":"title -fandom"}`:                            {seedWork},
		`{"q":"\"sentinel title\""}`:                       {seedWork, seedFetchedWork}, // best_match: fewer other words rank higher
		`{"q":"title","sort":"kudos","direction":"asc"}`:   {seedWork, seedFetchedWork},
		`{"q":"title","sort":"best_match","fetched":true}`: {seedFetchedWork},
		`{"q":"nomatch"}`:                                  {},
	} {
		l := queryWorks(t, h, "/api/v1/works"+revealQuery, body)
		assert.Equal(t, want, workIDs(t, l), body)
		assert.Equal(t, len(want), l.Total, body)
	}
	queryWorks(t, h, "/api/v1/works"+revealQuery, `{"q":"`+sentinel+`query"}`)
	assert.NotContains(t, logs.String(), sentinel, "q is never logged")
}

func TestQueryWorksRateLimit(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, func(o *runtime.Options) { o.SearchPerMin = 1 })
	queryWorks(t, a.Public(), "/api/v1/works", `{}`)
	w := queryJSON(t, a.Public(), "/api/v1/works", `{}`)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, dto.TypeRateLimited, testutil.DecodeProblem(t, w).Type)
	assert.NotEmpty(t, w.Header().Get("Retry-After"))
	assert.Equal(t, http.StatusOK, testutil.Get(t, a.Public(), "/api/v1/works").Code, "GET keeps the general limit")

	// The admin socket has a separate bucket with a 10x limit.
	for range 10 {
		queryWorks(t, a.Admin(), "/api/v1/works", `{}`)
	}
	assert.Equal(t, http.StatusTooManyRequests, queryJSON(t, a.Admin(), "/api/v1/works", `{}`).Code)
}
