package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

func workIDs(t *testing.T, l dto.List[dto.WorkBlurb]) []int64 {
	t.Helper()
	ids := make([]int64, len(l.Items))
	for i, w := range l.Items {
		ids[i] = w.ID
	}
	return ids
}

func TestListWorks(t *testing.T) {
	// A full page is one batch of blurb relations (controllers.ListWorks refuses more).
	require.LessOrEqual(t, runtime.MaxPerPage, store.MaxWorkBatch)

	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()

	l := getJSON[dto.List[dto.WorkBlurb]](t, h, "/api/v1/works")
	assert.Equal(t, 2, l.Total)
	assert.Equal(t, int64(1), l.Page)
	assert.Equal(t, int64(runtime.DefaultPerPage), l.PerPage)
	assert.Equal(t, []int64{seedFetchedWork, seedWork}, workIDs(t, l), "same revised day: id descending")

	w := l.Items[0]
	assert.Equal(t, dto.WorkStatus("ok"), w.Status)
	for _, s := range []string{w.Title, w.SummaryHTML, w.Language} {
		assert.True(t, isHash(t, s), "blurb text hashed")
	}
	assert.Equal(t, "2025-01-02", w.PublishedOn, "dates pass")
	assert.Equal(t, "2025-03-04", w.RevisedOn)
	assert.Equal(t, 3000, w.Words)
	assert.Equal(t, 3, w.ChaptersExpected)
	assert.Equal(t, dto.WorkStats{Kudos: 9, Hits: 90}, w.Stats)
	assert.Equal(t, seedTime, w.LastFetchedAt)
	require.Len(t, w.Creators, 1)
	assert.True(t, isHash(t, w.Creators[0].User) && isHash(t, w.Creators[0].Pseud), "creator names hashed")
	require.Len(t, w.Tags, 2)
	assert.Equal(t, []dto.TagType{"Rating", "Fandom"}, []dto.TagType{w.Tags[0].Type, w.Tags[1].Type}, "page order, types pass")
	assert.True(t, isHash(t, w.Tags[0].Name), "tag name hashed")
	require.Len(t, w.Series, 1)
	assert.Equal(t, seedSeries, w.Series[0].ID)
	assert.Equal(t, 2, w.Series[0].Position)
	assert.True(t, isHash(t, w.Series[0].Title), "series title hashed")

	// The blurb-only work: no series, fetch time or published date on the wire.
	var raw struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	b := testutil.Get(t, h, "/api/v1/works?sort=words&direction=asc&per_page=1")
	require.Equal(t, http.StatusOK, b.Code)
	require.NoError(t, json.Unmarshal(b.Body.Bytes(), &raw))
	require.Len(t, raw.Items, 1)
	assert.Equal(t, "7", string(raw.Items[0]["id"]))
	assert.Equal(t, "[]", string(raw.Items[0]["series"]))
	for _, key := range []string{"last_fetched_at", "published_on", "notes_html", "chapters", "collections"} {
		assert.NotContains(t, raw.Items[0], key)
	}

	for target, want := range map[string][]int64{
		"/api/v1/works?sort=words":                   {seedFetchedWork, seedWork}, // descending by default
		"/api/v1/works?sort=words&direction=asc":     {seedWork, seedFetchedWork},
		"/api/v1/works?sort=kudos&direction=asc":     {seedWork, seedFetchedWork},
		"/api/v1/works?sort=title":                   {seedWork, seedFetchedWork}, // ascending by default; equal keys by id
		"/api/v1/works?direction=asc":                {seedWork, seedFetchedWork}, // revised, ascending
		"/api/v1/works?page=2&per_page=1":            {seedWork},
		"/api/v1/works?page=3&per_page=1":            {},
		"/api/v1/works?sort=creator&direction=desc":  {seedFetchedWork, seedWork},
		"/api/v1/works?sort=bookmarks&direction=asc": {seedWork, seedFetchedWork},
	} {
		l := getJSON[dto.List[dto.WorkBlurb]](t, h, target)
		assert.Equal(t, want, workIDs(t, l), target)
		assert.Equal(t, 2, l.Total, "total counts every page: %s", target)
	}

	for target, field := range map[string]dto.ParamName{
		"/api/v1/works?sort=bogus":              dto.ParamSort,
		"/api/v1/works?sort=":                   dto.ParamSort,
		"/api/v1/works?sort=Words":              dto.ParamSort,
		"/api/v1/works?direction=up":            dto.ParamDirection,
		"/api/v1/works?per_page=101":            dto.ParamPerPage,
		"/api/v1/works?page=0":                  dto.ParamPage,
		"/api/v1/works?page=100002&per_page=1":  dto.ParamPage,
		"/api/v1/works?sort=words&sort=hits":    dto.ParamSort,
		"/api/v1/works?direction=asc&state=new": dto.ParamState,
	} {
		testutil.RequireParamProblem(t, testutil.Get(t, h, target), field, target)
	}
	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/works?language=x"), "", "unknown param")
	bm := testutil.Get(t, h, "/api/v1/works?sort=best_match")
	testutil.RequireParamProblem(t, bm, dto.ParamSort, "best_match needs q, which GET lacks")
	assert.Equal(t, request.DetailBestMatch.Message(), testutil.DecodeProblem(t, bm).Errors[0].Detail)

	// Every invalid parameter is listed in one response.
	bad := testutil.Get(t, h, "/api/v1/works?sort=x&direction=y&per_page=0")
	require.Equal(t, http.StatusUnprocessableEntity, bad.Code)
	p := testutil.DecodeProblem(t, bad)
	var params []dto.ParamName
	for _, e := range p.Errors {
		params = append(params, e.Parameter)
	}
	assert.Equal(t, []dto.ParamName{dto.ParamSort, dto.ParamDirection, dto.ParamPerPage}, params)
	assert.Equal(t, runtime.DetailWorkSort.Message(), p.Errors[0].Detail, "the detail lists the values")

	// Revealed: the real content.
	l = getJSON[dto.List[dto.WorkBlurb]](t, h, "/api/v1/works"+revealQuery)
	assert.Equal(t, sentinel+"title", l.Items[0].Title)
	assert.Equal(t, sentinel+"series", l.Items[0].Series[0].Title)
}
