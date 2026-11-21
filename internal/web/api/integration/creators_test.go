package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

// TestGetCreator reads seedCreator, the byline of seedWork and seedFetchedWork.
func TestGetCreator(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	work := getJSON[dto.Work](t, h, route.Path(route.Work, seedFetchedWork))
	path := route.Path(route.Creator, seedCreator)

	c := getJSON[dto.Creator](t, h, path)
	assert.Equal(t, dto.Creator{ID: seedCreator, User: work.Creators[0].User, Pseud: work.Creators[0].Pseud, Works: 2,
		MirrorSourceURL: c.MirrorSourceURL}, c, "the work view's hashes; both seeded works")
	assert.True(t, isHash(t, c.MirrorSourceURL), "hashed")

	r := getJSON[dto.Creator](t, h, path+revealQuery)
	assert.Equal(t, sentinel+"user", r.User)
	assert.Equal(t, sentinel+"pseud", r.Pseud)
	src, err := ao3url.Parse(r.MirrorSourceURL)
	require.NoError(t, err, "a source add accepts it")
	assert.Equal(t, ao3url.Source{Kind: ao3url.KindPseud, Target: sentinel + "user/" + sentinel + "pseud"}, src)

	w := testutil.Get(t, h, route.Path(route.Creator, 999))
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, runtime.DetailNoCreator.Message(), testutil.DecodeProblem(t, w).Detail)
	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/creators/0"), dto.ParamID, "id")
	testutil.RequireParamProblem(t, testutil.Get(t, h, path+"?page=2"), dto.ParamPage, "no paging here")
}

func TestCreatorWorks(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, func(o *runtime.Options) { o.SearchPerMin = 2 })
	h := a.Public()
	path := route.Path(route.CreatorWorks, seedCreator)

	g := testutil.Get(t, h, path)
	require.Equal(t, http.StatusOK, g.Code)
	assert.Equal(t, runtime.ContentTypeJSON, g.Header().Get(runtime.HeaderAcceptQuery), "GET advertises QUERY")
	l := getJSON[dto.List[dto.WorkBlurb]](t, h, path+"?sort=words")
	assert.Equal(t, []int64{seedFetchedWork, seedWork}, workIDs(t, l), "the creator's works, most words first")
	assert.Equal(t, getJSON[dto.Creator](t, h, route.Path(route.Creator, seedCreator)).Works, l.Total,
		"the creator view's count")
	assert.Empty(t, workIDs(t, getJSON[dto.List[dto.WorkBlurb]](t, h, path+"?page=2&per_page=2")))

	// QUERY: the body's filters within the creator's works.
	assert.Equal(t, []int64{seedFetchedWork}, workIDs(t, queryWorks(t, h, path, `{"fetched":true}`)))
	assert.Equal(t, []int64{seedWork}, workIDs(t, queryWorks(t, h, path, `{"fetched":false}`)))

	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/creators/x/works"), dto.ParamID, "bad id")
	testutil.RequireParamProblem(t, testutil.Get(t, h, path+"?sort=best_match"), dto.ParamSort, "best_match without q")
	testutil.RequireParamProblem(t, queryJSON(t, a.Admin(), "/api/v1/creators/0/works", `{}`), dto.ParamID, "query bad id")
	bad := testutil.Get(t, h, "/api/v1/creators/0/works?sort=x")
	require.Equal(t, http.StatusUnprocessableEntity, bad.Code)
	assert.Len(t, testutil.DecodeProblem(t, bad).Errors, 2, "the id and the sort in one response")

	nf := testutil.Get(t, h, route.Path(route.CreatorWorks, 999))
	require.Equal(t, http.StatusNotFound, nf.Code)
	assert.Equal(t, runtime.DetailNoCreator.Message(), testutil.DecodeProblem(t, nf).Detail)

	// QUERY shares the search budget with QUERY /works (2 per minute here, both spent above).
	w := queryJSON(t, h, "/api/v1/works", `{}`)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
}

// queryCreators sends a creator query, requires 200 and decodes the page.
func queryCreators(t *testing.T, h http.Handler, target, body string) dto.List[dto.Creator] {
	t.Helper()
	w := queryJSON(t, h, target, body)
	require.Equal(t, http.StatusOK, w.Code, body+": "+w.Body.String())
	assert.Equal(t, runtime.ContentTypeJSON, w.Header().Get(runtime.HeaderAcceptQuery), "QUERY advertised")
	var l dto.List[dto.Creator]
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &l))
	return l
}

func TestQueryCreators(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	view := getJSON[dto.Creator](t, h, route.Path(route.Creator, seedCreator))

	all := queryCreators(t, h, route.Creators, `{}`)
	assert.Equal(t, dto.List[dto.Creator]{Items: []dto.Creator{view}, Page: 1, PerPage: runtime.DefaultPerPage, Total: 1},
		all, "the one seeded creator, as its view")
	byWorks := queryCreators(t, h, route.Creators+"?per_page=1", `{"sort":"works","direction":"asc"}`)
	assert.Equal(t, []dto.Creator{view}, byWorks.Items)
	assert.Empty(t, queryCreators(t, h, route.Creators+"?page=2", `{}`).Items, "past the last page")

	// Every invalid parameter and member is listed in one response.
	bad := queryJSON(t, h, route.Creators+"?per_page=0", `{"sort":"uses","direction":"up"}`)
	require.Equal(t, http.StatusUnprocessableEntity, bad.Code, bad.Body.String())
	var where []string
	for _, e := range testutil.DecodeProblem(t, bad).Errors {
		where = append(where, string(e.Parameter)+e.Pointer)
	}
	assert.Equal(t, []string{"per_page", "/sort", "/direction"}, where,
		"pointers pass the enforcer (they name CreatorQuery members)")

	// The name is free text: refused hashed, before the store is read.
	logs.Reset()
	w := queryJSON(t, h, route.Creators, `{"name":"`+sentinel+`pseud"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	p := testutil.DecodeProblem(t, w)
	assert.Equal(t, dto.TypeRevealRequired, p.Type)
	assert.Equal(t, runtime.DetailFreeText.Message(), p.Detail)
	testutil.AssertNoSentinel(t, w, logs, sentinel)
	assert.Equal(t, dto.TypeValidation, testutil.DecodeProblem(t, queryJSON(t, h, route.Creators, `{"name":" "}`)).Type,
		"an invalid name is a validation error, hashed too")

	// Revealed: name words match the pseud or the login, ignoring case.
	l := queryCreators(t, h, route.Creators+revealQuery, `{"name":"PSEUD `+sentinel+`user"}`)
	require.Len(t, l.Items, 1)
	assert.Equal(t, sentinel+"pseud", l.Items[0].Pseud)
	assert.Empty(t, queryCreators(t, h, route.Creators+revealQuery, `{"name":"`+sentinel+`pseud nomatch"}`).Items,
		"every word must match")
	assert.NotContains(t, logs.String(), sentinel, "the name is never logged")
}
