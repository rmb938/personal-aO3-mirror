package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

func TestGetWork(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()

	w := getJSON[dto.Work](t, h, "/api/v1/works/9")
	assert.Equal(t, seedFetchedWork, w.ID)
	assert.Equal(t, dto.WorkStatus("ok"), w.Status)
	for _, s := range []string{w.Title, w.SummaryHTML, w.NotesHTML, w.EndNotesHTML, w.Language} {
		assert.True(t, isHash(t, s), "work text hashed")
	}
	assert.Equal(t, "2025-01-02", w.PublishedOn, "dates pass")
	assert.Equal(t, "2025-03-04", w.RevisedOn)
	assert.Equal(t, 3000, w.Words)
	assert.Equal(t, 3, w.ChaptersExpected)
	assert.Equal(t, dto.WorkStats{Kudos: 9, Hits: 90}, w.Stats)
	assert.Equal(t, seedTime, w.LastFetchedAt)

	require.Len(t, w.Creators, 1)
	assert.NotZero(t, w.Creators[0].ID)
	assert.True(t, isHash(t, w.Creators[0].User) && isHash(t, w.Creators[0].Pseud), "creator names hashed")
	require.Len(t, w.Tags, 2)
	assert.Equal(t, []dto.TagType{"Rating", "Fandom"}, []dto.TagType{w.Tags[0].Type, w.Tags[1].Type}, "page order, types pass")
	assert.True(t, isHash(t, w.Tags[0].Name), "tag name hashed")
	require.Len(t, w.Collections, 1)
	assert.True(t, isHash(t, w.Collections[0].Name) && isHash(t, w.Collections[0].Title), "collection hashed")

	require.Len(t, w.Series, 1)
	s := w.Series[0]
	assert.True(t, isHash(t, s.Title), "series title hashed")
	s.Title = ""
	assert.Equal(t, dto.WorkSeries{ID: seedSeries, Position: 2, CompleteLocally: false, WorksTotal: new(3),
		WorksMirrored: 1, MissingPositions: []int{1, 3}, MissingWorkIDs: []int64{seedSeriesOther}}, s, "partial series flagged")

	require.Len(t, w.Chapters, 2)
	c := w.Chapters[1]
	assert.Equal(t, int64(92), c.ID)
	assert.Equal(t, 2, c.Position)
	assert.Equal(t, "2025-03-04", c.PublishedOn)
	assert.True(t, isHash(t, c.Title), "chapter title hashed")

	// A blurb-only work: no chapters or fetch time, lists are [] on the wire.
	var raw map[string]json.RawMessage
	b := testutil.Get(t, h, "/api/v1/works/7")
	require.Equal(t, http.StatusOK, b.Code)
	require.NoError(t, json.Unmarshal(b.Body.Bytes(), &raw))
	assert.Equal(t, "[]", string(raw["chapters"]))
	assert.Equal(t, "[]", string(raw["series"]))
	assert.NotContains(t, raw, "last_fetched_at")
	assert.NotContains(t, raw, "published_on")

	nf := testutil.Get(t, h, "/api/v1/works/8")
	require.Equal(t, http.StatusNotFound, nf.Code)
	assert.Equal(t, runtime.DetailNoWork.Message(), testutil.DecodeProblem(t, nf).Detail)
	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/works/x"), dto.ParamID, "bad id")
	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/works/9?verbose=1"), "", "unknown param")

	// Revealed: the real content.
	w = getJSON[dto.Work](t, h, "/api/v1/works/9"+revealQuery)
	assert.Equal(t, sentinel+"title", w.Title)
	assert.Equal(t, sentinel+"chapter2", w.Chapters[1].Title)
}

func TestGetWorkSkin(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()

	s := getJSON[dto.WorkSkin](t, h, "/api/v1/works/9/skin")
	assert.True(t, isHash(t, s.CSS), "skin hashed")
	s = getJSON[dto.WorkSkin](t, h, "/api/v1/works/9/skin"+revealQuery)
	assert.Equal(t, "#workskin ."+sentinel+"skin { color: red; }", s.CSS, "CSS is not escaped")

	none := testutil.Get(t, h, "/api/v1/works/7/skin")
	require.Equal(t, http.StatusOK, none.Code)
	assert.JSONEq(t, `{}`, none.Body.String(), "no skin: css omitted")

	nf := testutil.Get(t, h, "/api/v1/works/8/skin")
	require.Equal(t, http.StatusNotFound, nf.Code)
	assert.Equal(t, runtime.DetailNoWork.Message(), testutil.DecodeProblem(t, nf).Detail)
}
