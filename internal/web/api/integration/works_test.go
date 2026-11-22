package integration

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

func TestGetWork(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := signedIn(t, a)

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

func TestGetChapter(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := signedIn(t, a)

	c := getJSON[dto.Chapter](t, h, "/api/v1/works/9/chapters/2")
	assert.Equal(t, int64(92), c.ID)
	assert.Equal(t, 2, c.Position)
	assert.Equal(t, "2025-03-04", c.PublishedOn, "dates pass")
	for _, s := range []string{c.Title, c.BodyHTML, c.NotesHTML, c.EndNotesHTML} {
		assert.True(t, isHash(t, s), "chapter text hashed")
	}
	assert.Empty(t, c.SummaryHTML, "no chapter summary: omitted")

	c = getJSON[dto.Chapter](t, h, "/api/v1/works/9/chapters/2"+revealQuery)
	assert.Equal(t, sentinel+"body2", c.BodyHTML)
	assert.Equal(t, sentinel+"chend", c.EndNotesHTML)

	for _, target := range []string{"/api/v1/works/9/chapters/3", "/api/v1/works/7/chapters/1"} {
		nf := testutil.Get(t, h, target)
		require.Equal(t, http.StatusNotFound, nf.Code, target)
		assert.Equal(t, runtime.DetailNoChapter.Message(), testutil.DecodeProblem(t, nf).Detail, "no body stored")
	}
	nf := testutil.Get(t, h, "/api/v1/works/8/chapters/1")
	require.Equal(t, http.StatusNotFound, nf.Code)
	assert.Equal(t, runtime.DetailNoWork.Message(), testutil.DecodeProblem(t, nf).Detail)

	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/works/9/chapters/0"), dto.ParamPosition, "position 0")
	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/works/9/chapters/100001"), dto.ParamPosition, "over max")
	bad := testutil.Get(t, h, "/api/v1/works/x/chapters/y")
	require.Equal(t, http.StatusUnprocessableEntity, bad.Code)
	assert.Len(t, testutil.DecodeProblem(t, bad).Errors, 2, "both path params reported")
}

func TestGetWorkChapters(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := signedIn(t, a)

	all := getJSON[dto.WorkChapters](t, h, "/api/v1/works/9/chapters")
	require.Len(t, all.Chapters, 2)
	assert.Equal(t, []int{1, 2}, []int{all.Chapters[0].Position, all.Chapters[1].Position})
	assert.True(t, isHash(t, all.Chapters[0].BodyHTML), "body hashed")
	all = getJSON[dto.WorkChapters](t, h, "/api/v1/works/9/chapters"+revealQuery)
	assert.Equal(t, sentinel+"body1", all.Chapters[0].BodyHTML)
	assert.Equal(t, sentinel+"chapter2", all.Chapters[1].Title)

	none := testutil.Get(t, h, "/api/v1/works/7/chapters")
	require.Equal(t, http.StatusOK, none.Code)
	assert.JSONEq(t, `{"chapters":[]}`, none.Body.String(), "blurb-only work: no chapters")

	nf := testutil.Get(t, h, "/api/v1/works/8/chapters")
	require.Equal(t, http.StatusNotFound, nf.Code)
	assert.Equal(t, runtime.DetailNoWork.Message(), testutil.DecodeProblem(t, nf).Detail)
	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/works/9/chapters?verbose=1"), "", "unknown param")
}

// TestGetWorkChaptersTooLarge: a work over the size cap is refused whole with 422; its chapters
// still read one at a time.
func TestGetWorkChaptersTooLarge(t *testing.T) {
	big := func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.PutWorkContent(ctx, seedWork, []model.Chapter{
			{Position: 1, BodyHTML: strings.Repeat("a", controllers.MaxWorkChaptersBytes)},
			{Position: 2, BodyHTML: "<p>lorem</p>"},
		}, "")
		return err
	}
	a, _ := testutil.NewAPI(t, testOptions, func(t *testing.T, opts runtime.Options, log *slog.Logger) (*runtime.API, error) {
		t.Helper()
		return newAPI(t, opts, log, newTestControllers(t, log, big))
	}, nil)
	h := signedIn(t, a)

	w := testutil.Get(t, h, route.Path(route.Chapters, seedWork))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	p := testutil.DecodeProblem(t, w)
	assert.Equal(t, dto.TypeWorkTooLarge, p.Type)
	assert.Equal(t, runtime.DetailWorkTooLarge.Message(), p.Detail)
	assert.Equal(t, http.StatusOK, testutil.Get(t, h, route.Path(route.Chapter, seedWork, 2)).Code, "one chapter reads")
}

func TestGetWorkSkin(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := signedIn(t, a)

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
