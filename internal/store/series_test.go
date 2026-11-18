package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// markSeriesSource turns series 77 into a series source (sources are created as tags by addSource).
func markSeriesSource(t *testing.T, s *testStore) {
	t.Helper()
	addSource(t, s, "77", model.SourceActive, 0)
	_, err := s.meta.Exec(`UPDATE sources SET kind = 'series' WHERE target = '77'`)
	require.NoError(t, err)
}

func TestPartialSeries(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	// Fetched parts 1 and 3 of a 4-part series; work 900 is only a blurb (never fetched).
	w1 := fixtureWork(1)
	w3 := fixtureWork(3)
	w3.Series = []model.SeriesPart{{ID: 77, Position: 3}}
	for _, u := range []struct {
		w      model.Work
		origin model.Origin
	}{{w1, model.FromPage}, {w3, model.FromPage}, {fixtureWork(900), model.FromBlurb}} {
		_, err := s.UpsertWork(ctx, u.w, u.origin, 1)
		require.NoError(t, err)
	}
	ps, err := s.PartialSeriesList(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, ps, "series without info (unknown total) must not be listed")

	require.NoError(t, s.UpsertSeries(ctx, model.Series{ID: 77, WorksCount: 4}))
	members := func(ids ...int64) []SeriesMember {
		out := make([]SeriesMember, len(ids))
		for i, id := range ids {
			out[i] = SeriesMember{WorkID: id, Position: int(id)}
		}
		return out
	}
	require.NoError(t, s.SetSeriesMembers(ctx, 77, members(1, 2, 3, 4)))
	ps, err = s.PartialSeriesList(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, []PartialSeries{{ID: 77, WorksTotal: 4, Mirrored: 2, MissingPositions: []int{2, 4},
		MissingWorkIDs: []int64{2, 4}}}, ps)

	// Re-seeing the page replaces unfetched members and keeps fetched ones.
	require.NoError(t, s.SetSeriesMembers(ctx, 77, members(1, 3, 5)))
	ps, err = s.PartialSeriesList(ctx, 10)
	require.NoError(t, err)
	require.Len(t, ps, 1)
	require.Equal(t, []int64{5}, ps[0].MissingWorkIDs, "members replaced")
	require.Equal(t, 2, ps[0].Mirrored)

	// Unfetched members never count as mirrored, and other series_works users ignore them
	// (joins to works drop rows with no works row).
	var n int
	require.NoError(t, s.meta.QueryRow(`SELECT COUNT(*) FROM series_works sw JOIN works w ON w.id = sw.work_id
		WHERE sw.series_id = 77`).Scan(&n))
	require.Equal(t, 2, n)

	// A series that is a source is mirrored in full by its own polls, so it is not partial.
	markSeriesSource(t, s)
	ps, err = s.PartialSeriesList(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, ps, "source series listed as partial")
}

func TestSeriesUpsertAndNeedingInfo(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	w := fixtureWork(1) // in series 77, revised 2026-11-01
	_, err := s.UpsertWork(ctx, w, model.FromBlurb, 1)
	require.NoError(t, err)

	needing := func(before time.Time) []int64 {
		t.Helper()
		ids, err := s.SeriesNeedingInfo(ctx, before, 10)
		require.NoError(t, err)
		return ids
	}
	require.Equal(t, []int64{77}, needing(clk.t.Add(-30*24*time.Hour)), "never-fetched series should need info")

	ser := model.Series{ID: 77, Title: "Fixture Series", SummaryHTML: "<p>fixture description</p>", NotesHTML: "<p>n</p>",
		Begun: "2026-01-01", Updated: "2026-11-01", Words: 1234, WorksCount: 2, Complete: false, Bookmarks: 3,
		Creators: []model.Creator{{User: "fixture_author", Pseud: "fixture_author"}}}
	require.NoError(t, s.UpsertSeries(ctx, ser))
	var desc string
	var words, count, creators int
	require.NoError(t, s.meta.QueryRow(`SELECT summary_html, words, works_count FROM series WHERE id = 77`).Scan(&desc, &words, &count))
	require.NoError(t, s.meta.QueryRow(`SELECT COUNT(*) FROM series_creators WHERE series_id = 77`).Scan(&creators))
	require.Equal(t, ser.SummaryHTML, desc)
	require.Equal(t, 1234, words)
	require.Equal(t, 2, count)
	require.Equal(t, 1, creators)
	require.Empty(t, needing(clk.t.Add(-30*24*time.Hour)), "fresh series should not need info")

	// A member work revised after the series fetch makes it stale again.
	clk.t = clk.t.Add(time.Hour)
	w.Revised, w.ChaptersPosted = "2026-12-01", 3
	_, err = s.UpsertWork(ctx, w, model.FromBlurb, 1)
	require.NoError(t, err)
	require.Len(t, needing(clk.t.Add(-30*24*time.Hour)), 1, "series with a newer member work should need info")

	// Fetched again: the member's revised date (2026-12-01) is still ahead of the clock, as a
	// blurb date a day ahead of UTC is. That must not keep the series due.
	require.NoError(t, s.MarkSeriesFetched(ctx, 77))
	require.True(t, clk.t.Before(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)), "test needs a revised date ahead of the clock")
	require.Empty(t, needing(clk.t.Add(-30*24*time.Hour)), "future-dated revision kept the series due after a fetch")

	// Series that are sources are refreshed by their polls instead.
	markSeriesSource(t, s)
	require.Empty(t, needing(clk.t.Add(time.Hour)), "source series must be excluded")
}
