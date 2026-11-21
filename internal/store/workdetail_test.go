package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// detailWork is fixtureWork(1) with two creators, two collections, two series and a chapter list.
func detailWork() model.Work {
	w := fixtureWork(1)
	w.Creators = append(w.Creators, model.Creator{User: "fixture_second", Pseud: "fixture_second"})
	w.Collections = []model.Collection{{Name: "fixture_coll_b", Title: "Fixture Coll B"}, {Name: "fixture_coll_a"}}
	w.Series = []model.SeriesPart{{ID: 88, Title: "Fixture Series B", Position: 2}, {ID: 77, Title: "Fixture Series", Position: 1}}
	w.Published = "2026-10-01"
	w.Chapters = []model.Chapter{
		{ID: 501, Position: 1, Title: "Fixture Chapter One", Published: "2026-10-01"},
		{ID: 502, Position: 2, Published: "2026-11-01"},
	}
	return w
}

func TestWorkRelations(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertWork(ctx, detailWork(), model.FromPage, 1)
	require.NoError(t, err)

	r, err := s.Work(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "Fixture Title Alpha", r.Title)
	require.Equal(t, model.StatusOK, r.Status)
	require.Equal(t, clk.t.Unix(), r.LastFetched.Unix(), "a page upsert marks the work fetched")
	require.Nil(t, r.Creators, "Work loads the row only")

	creators, err := s.WorkCreators(ctx, 1)
	require.NoError(t, err)
	require.Len(t, creators, 2)
	require.Equal(t, []string{"fixture_author", "fixture_second"}, []string{creators[0].User, creators[1].User}, "byline order")
	require.Equal(t, "fixture_pseud", creators[0].Pseud)
	require.NotZero(t, creators[0].ID)

	tags, err := s.WorkTags(ctx, 1)
	require.NoError(t, err)
	require.Len(t, tags, 4)
	for i, want := range fixtureWork(1).Tags {
		require.Equal(t, want.Name, tags[i].Name, "page order")
		require.Equal(t, want.Type, tags[i].Type)
	}

	colls, err := s.WorkCollections(ctx, 1)
	require.NoError(t, err)
	require.Len(t, colls, 2)
	require.Equal(t, "fixture_coll_b", colls[0].Name, "by collection id (first stored first)")
	require.Equal(t, "Fixture Coll B", colls[0].Title)
	require.Empty(t, colls[1].Title)

	chs, err := s.WorkChapters(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, detailWork().Chapters, chs)

	series, err := s.WorkSeries(ctx, 1)
	require.NoError(t, err)
	require.Len(t, series, 2)
	require.Equal(t, int64(77), series[0].ID, "by series id")
	require.Equal(t, "Fixture Series", series[0].Title)
	require.Equal(t, 1, series[0].Position)
	require.Equal(t, 2, series[1].Position)

	// A work with no row has no relations.
	_, err = s.Work(ctx, 2)
	require.ErrorIs(t, err, ErrNoWork)
	creators, err = s.WorkCreators(ctx, 2)
	require.NoError(t, err)
	require.Empty(t, creators)
}

// A work view's series coverage follows the partial-series rule of PartialSeriesList.
func TestWorkSeriesCoverage(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertWork(ctx, fixtureWork(1), model.FromPage, 1) // part 1 of series 77
	require.NoError(t, err)
	coverage := func() SeriesCoverage {
		t.Helper()
		parts, err := s.WorkSeries(ctx, 1)
		require.NoError(t, err)
		require.Len(t, parts, 1)
		return parts[0].Coverage
	}

	c := coverage()
	require.Equal(t, SeriesCoverage{Mirrored: 1}, c, "series page never fetched: total unknown")
	require.False(t, c.Partial())

	require.NoError(t, s.UpsertSeries(ctx, model.Series{ID: 77, WorksCount: 3}))
	require.NoError(t, s.SetSeriesMembers(ctx, 77, []SeriesMember{{WorkID: 1, Position: 1}, {WorkID: 2, Position: 2},
		{WorkID: 3, Position: 3}}))
	c = coverage()
	require.Equal(t, SeriesCoverage{Fetched: true, WorksTotal: 3, Mirrored: 1, MissingPositions: []int{2, 3},
		MissingWorkIDs: []int64{2, 3}}, c)
	require.True(t, c.Partial())
	ps, err := s.PartialSeriesList(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, []PartialSeries{{ID: 77, WorksTotal: 3, Mirrored: 1, MissingPositions: []int{2, 3},
		MissingWorkIDs: []int64{2, 3}}}, ps, "same answer as the status list")

	markSeriesSource(t, s)
	c = coverage()
	require.True(t, c.Source)
	require.False(t, c.Partial(), "a source series is mirrored by its own polls")
}

func TestChapterBodyAndSkin(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	skin, err := s.WorkSkin(ctx, 9)
	require.NoError(t, err)
	require.Empty(t, skin)

	in := []model.Chapter{
		{ID: 1, Position: 1, BodyHTML: "<p>fixture one</p>"},
		{ID: 2, Position: 2, BodyHTML: "<p>fixture two</p>", NotesHTML: "<p>n</p>", EndNotesHTML: "<p>e</p>", SummaryHTML: "<p>s</p>"},
	}
	_, err = s.PutWorkContent(ctx, 9, in, "#workskin p { color: red; }")
	require.NoError(t, err)

	c, err := s.ChapterBody(ctx, 9, 2)
	require.NoError(t, err)
	require.Equal(t, in[1], c)
	_, err = s.ChapterBody(ctx, 9, 3)
	require.ErrorIs(t, err, ErrNoChapter)
	_, err = s.ChapterBody(ctx, 10, 1)
	require.ErrorIs(t, err, ErrNoChapter)

	skin, err = s.WorkSkin(ctx, 9)
	require.NoError(t, err)
	require.Equal(t, "#workskin p { color: red; }", skin)
}
