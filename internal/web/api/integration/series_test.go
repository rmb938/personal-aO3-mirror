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

// TestGetSeries reads the seed's partial series: 3 works on AO3, seedFetchedWork (part 2)
// fetched, seedSeriesOther (part 3) known from the series page only.
func TestGetSeries(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := signedIn(t, a)
	work := getJSON[dto.Work](t, h, route.Path(route.Work, seedFetchedWork))
	path := route.Path(route.Series, seedSeries)

	s := getJSON[dto.Series](t, h, path)
	assert.Equal(t, seedSeries, s.ID)
	assert.Equal(t, work.Series[0].Title, s.Title, "hashed, the same hash as the work view's series")
	for _, v := range []string{s.Title, s.SummaryHTML, s.NotesHTML, s.MirrorSourceURL} {
		assert.True(t, isHash(t, v), "hashed")
	}
	assert.Equal(t, work.Creators, s.Creators, "the work's creator, the same hashes")
	assert.Equal(t, "2025-01-02", s.BegunOn)
	assert.Equal(t, "2025-03-04", s.UpdatedOn)
	assert.Equal(t, 3000, s.Words)
	assert.Equal(t, 2, s.Bookmarks)
	assert.False(t, s.CompleteLocally, "partial")
	assert.Equal(t, new(3), s.WorksTotal)
	assert.Equal(t, 1, s.WorksMirrored)
	assert.Equal(t, []int{1, 3}, s.MissingPositions)
	assert.Equal(t, []int64{seedSeriesOther}, s.MissingWorkIDs)
	assert.Equal(t, 1, s.WorksStored, "seedSeriesOther has no works row")
	assert.Equal(t, seedTime, s.LastFetchedAt)

	r := getJSON[dto.Series](t, h, path+revealQuery)
	assert.Equal(t, sentinel+"series", r.Title)
	assert.Equal(t, sentinel+"seriessummary", r.SummaryHTML)
	assert.Equal(t, sentinel+"seriesnotes", r.NotesHTML)
	src, err := ao3url.Parse(r.MirrorSourceURL)
	require.NoError(t, err, "a source add accepts it")
	assert.Equal(t, ao3url.Source{Kind: ao3url.KindSeries, Target: "77"}, src)

	w := testutil.Get(t, h, route.Path(route.Series, 999))
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, runtime.DetailNoSeries.Message(), testutil.DecodeProblem(t, w).Detail)
	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/series/0"), dto.ParamID, "id")
	testutil.RequireParamProblem(t, testutil.Get(t, h, path+"?page=2"), dto.ParamPage, "no paging here")
}

func TestListSeriesWorks(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := signedIn(t, a)
	path := route.Path(route.SeriesWorks, seedSeries)

	l := getJSON[dto.List[dto.WorkBlurb]](t, h, path)
	assert.Equal(t, 1, l.Total)
	assert.Equal(t, int64(1), l.Page)
	assert.Equal(t, int64(runtime.DefaultPerPage), l.PerPage)
	require.Len(t, l.Items, 1)
	assert.Equal(t, seedFetchedWork, l.Items[0].ID)
	assert.True(t, isHash(t, l.Items[0].Title))
	assert.Equal(t, []dto.WorkSeriesPart{{ID: seedSeries, Title: l.Items[0].Series[0].Title, Position: 2}},
		l.Items[0].Series)
	assert.Empty(t, getJSON[dto.List[dto.WorkBlurb]](t, h, path+"?page=2").Items, "past the last page")

	l = getJSON[dto.List[dto.WorkBlurb]](t, h, path+revealQuery)
	assert.Equal(t, sentinel+"title", l.Items[0].Title)

	w := testutil.Get(t, h, route.Path(route.SeriesWorks, 999))
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, runtime.DetailNoSeries.Message(), testutil.DecodeProblem(t, w).Detail)

	// Every invalid parameter is listed in one response.
	bad := testutil.Get(t, h, "/api/v1/series/0/works?page=0&per_page=101")
	require.Equal(t, http.StatusUnprocessableEntity, bad.Code, bad.Body.String())
	var params []dto.ParamName
	for _, e := range testutil.DecodeProblem(t, bad).Errors {
		params = append(params, e.Parameter)
	}
	assert.Equal(t, []dto.ParamName{dto.ParamID, dto.ParamPerPage, dto.ParamPage}, params)
	testutil.RequireParamProblem(t, testutil.Get(t, h, path+"?sort=title"), dto.ParamSort, "no sort here")
}
