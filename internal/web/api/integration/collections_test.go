package integration

import (
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

// TestGetCollection reads seedCollection, which holds seedFetchedWork.
func TestGetCollection(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := signedIn(t, a)
	work := getJSON[dto.Work](t, h, route.Path(route.Work, seedFetchedWork))
	path := route.Path(route.Collection, seedCollection)

	c := getJSON[dto.Collection](t, h, path)
	assert.Equal(t, dto.Collection{ID: seedCollection, Name: work.Collections[0].Name, Title: work.Collections[0].Title,
		Works: 1, MirrorSourceURL: c.MirrorSourceURL}, c, "the work view's hashes; the one seeded work")
	assert.True(t, isHash(t, c.MirrorSourceURL), "hashed")

	r := getJSON[dto.Collection](t, h, path+revealQuery)
	assert.Equal(t, sentinel+"collname", r.Name)
	assert.Equal(t, sentinel+"colltitle", r.Title)
	src, err := ao3url.Parse(r.MirrorSourceURL)
	require.NoError(t, err, "a source add accepts it")
	assert.Equal(t, ao3url.Source{Kind: ao3url.KindCollection, Target: sentinel + "collname"}, src)

	w := testutil.Get(t, h, route.Path(route.Collection, 999))
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, runtime.DetailNoCollection.Message(), testutil.DecodeProblem(t, w).Detail)
	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/collections/0"), dto.ParamID, "id")
	testutil.RequireParamProblem(t, testutil.Get(t, h, path+"?page=2"), dto.ParamPage, "no paging here")
}

func TestCollectionWorks(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, func(o *runtime.Options) { o.SearchPerMin = 2 })
	h := signedIn(t, a)
	path := route.Path(route.CollectionWorks, seedCollection)

	g := testutil.Get(t, h, path)
	require.Equal(t, http.StatusOK, g.Code)
	assert.Equal(t, runtime.ContentTypeJSON, g.Header().Get(runtime.HeaderAcceptQuery), "GET advertises QUERY")
	l := getJSON[dto.List[dto.WorkBlurb]](t, h, path+"?sort=words")
	assert.Equal(t, []int64{seedFetchedWork}, workIDs(t, l), "the collection's works, not seedWork")
	assert.Equal(t, getJSON[dto.Collection](t, h, route.Path(route.Collection, seedCollection)).Works, l.Total,
		"the collection view's count")
	assert.Empty(t, workIDs(t, getJSON[dto.List[dto.WorkBlurb]](t, h, path+"?page=2&per_page=1")))

	// QUERY: the body's filters within the collection's works.
	assert.Equal(t, []int64{seedFetchedWork}, workIDs(t, queryWorks(t, h, path, `{"fetched":true}`)))
	assert.Empty(t, workIDs(t, queryWorks(t, h, path, `{"fetched":false}`)))

	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/collections/x/works"), dto.ParamID, "bad id")
	testutil.RequireParamProblem(t, testutil.Get(t, h, path+"?sort=best_match"), dto.ParamSort, "best_match without q")
	testutil.RequireParamProblem(t, queryJSON(t, a.Admin(), "/api/v1/collections/0/works", `{}`), dto.ParamID,
		"query bad id")
	bad := testutil.Get(t, h, "/api/v1/collections/0/works?sort=x")
	require.Equal(t, http.StatusUnprocessableEntity, bad.Code)
	assert.Len(t, testutil.DecodeProblem(t, bad).Errors, 2, "the id and the sort in one response")

	nf := testutil.Get(t, h, route.Path(route.CollectionWorks, 999))
	require.Equal(t, http.StatusNotFound, nf.Code)
	assert.Equal(t, runtime.DetailNoCollection.Message(), testutil.DecodeProblem(t, nf).Detail)

	// QUERY shares the search budget with QUERY /works (2 per minute here, both spent above).
	w := queryJSON(t, h, "/api/v1/works", `{}`)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
}
