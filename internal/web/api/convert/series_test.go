package convert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestSeriesView(t *testing.T) {
	fetched := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	v := controllers.SeriesView{
		Series: store.SeriesRow{Series: model.Series{ID: 77, Title: "Fixture Series", Begun: "2020-01-01",
			Updated: "2021-02-03", SummaryHTML: "<p>lorem</p>", NotesHTML: "<p>ipsum</p>", Words: 900, WorksCount: 3,
			Complete: true, Bookmarks: 4}, LastFetched: fetched},
		Creators: []store.CreatorRef{{ID: 5, User: "fixture_author", Pseud: "fixture_pseud"}},
		Coverage: store.SeriesCoverage{Fetched: true, WorksTotal: 3, Mirrored: 1, MissingPositions: []int{2, 3},
			MissingWorkIDs: []int64{12}},
		Works: 2,
	}
	got := SeriesView(v)
	assert.Equal(t, dto.Series{
		ID: 77, Title: "Fixture Series", Creators: []dto.WorkCreator{{ID: 5, User: "fixture_author", Pseud: "fixture_pseud"}},
		SummaryHTML: "<p>lorem</p>", NotesHTML: "<p>ipsum</p>", BegunOn: "2020-01-01", UpdatedOn: "2021-02-03",
		Words: 900, Bookmarks: 4, Complete: true, CompleteLocally: false, WorksTotal: new(3), WorksMirrored: 1,
		MissingPositions: []int{2, 3}, MissingWorkIDs: []int64{12}, WorksStored: 2,
		MirrorSourceURL: seriesSourceURL(77), LastFetchedAt: fetched.UTC(),
	}, got)

	v.Coverage.Source = true
	got = SeriesView(v)
	assert.True(t, got.CompleteLocally, "a source series is not partial")
	assert.Equal(t, seriesSourceURL(77), got.MirrorSourceURL, "set for a source too (a re-add is refused)")
}

func TestSeriesViewUnfetched(t *testing.T) {
	got := SeriesView(controllers.SeriesView{Series: store.SeriesRow{Series: model.Series{ID: 8, Title: "Fixture Series"}}})
	assert.Equal(t, dto.Series{ID: 8, Title: "Fixture Series", CompleteLocally: true, MirrorSourceURL: seriesSourceURL(8)},
		got, "a series named by a work page only has no total, dates or fetch time")
}

func TestSeriesSourceURL(t *testing.T) {
	src, err := ao3url.Parse(seriesSourceURL(77))
	require.NoError(t, err, "a source add accepts the URL")
	assert.Equal(t, ao3url.Source{Kind: ao3url.KindSeries, Target: "77"}, src)
}
