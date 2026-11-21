package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// seedViewSeries stores series 77 of 4 parts: works 2 (part 2) and 1 (part 1) from their pages,
// work 3 (part 3) from a blurb only; part 4 (work 9) is only on the series page. Series 78 is
// named by work 1 but never fetched.
func seedViewSeries(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, e.st.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		for _, u := range []struct {
			id, pos int
			origin  model.Origin
		}{{2, 2, model.FromPage}, {1, 1, model.FromPage}, {3, 3, model.FromBlurb}} {
			w := model.Work{ID: int64(u.id), Title: "Fixture Title", Revised: "2026-11-01",
				Creators: []model.Creator{{User: "fixture_author", Pseud: "fixture_author"}},
				Series:   []model.SeriesPart{{ID: 77, Title: "Fixture Series", Position: u.pos}}}
			if u.id == 1 {
				w.Series = append(w.Series, model.SeriesPart{ID: 78, Title: "Fixture Series B", Position: 1})
			}
			if _, err := tx.UpsertWork(ctx, w, u.origin, 1); err != nil {
				return err
			}
		}
		if err := tx.UpsertSeries(ctx, model.Series{ID: 77, Title: "Fixture Series", WorksCount: 4,
			Creators: []model.Creator{{User: "fixture_author", Pseud: "fixture_author"}}}); err != nil {
			return err
		}
		return tx.SetSeriesMembers(ctx, 77, []store.SeriesMember{{WorkID: 3, Position: 3}, {WorkID: 9, Position: 4}})
	}))
}

func TestGetSeries(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	seedViewSeries(t, e)

	v, err := e.svc.GetSeries(ctx, 77)
	require.NoError(t, err)
	assert.Equal(t, "Fixture Series", v.Series.Title)
	assert.Equal(t, 4, v.Series.WorksCount)
	assert.False(t, v.Series.LastFetched.IsZero())
	require.Len(t, v.Creators, 1)
	assert.Equal(t, "fixture_author", v.Creators[0].Pseud)
	assert.Equal(t, store.SeriesCoverage{Fetched: true, WorksTotal: 4, Mirrored: 2, MissingPositions: []int{3, 4},
		MissingWorkIDs: []int64{3, 9}}, v.Coverage, "the blurb-only work is not mirrored")
	assert.True(t, v.Coverage.Partial())
	assert.Equal(t, 3, v.Works, "every stored work, the blurb-only one too")

	b, err := e.svc.GetSeries(ctx, 78)
	require.NoError(t, err, "a series named by a work page")
	assert.Equal(t, "Fixture Series B", b.Series.Title)
	assert.True(t, b.Series.LastFetched.IsZero())
	assert.Empty(t, b.Creators)
	assert.False(t, b.Coverage.Fetched)
	assert.False(t, b.Coverage.Partial(), "no known total")
	assert.Equal(t, 1, b.Works)

	_, err = e.svc.GetSeries(ctx, 999)
	require.ErrorIs(t, err, store.ErrNoSeries)
}

func TestListSeriesWorks(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	seedViewSeries(t, e)
	ids := func(page []WorkBlurb) []int64 {
		var out []int64
		for _, b := range page {
			out = append(out, b.Work.ID)
		}
		return out
	}

	page, total, err := e.svc.ListSeriesWorks(ctx, 77, 0, 10)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Equal(t, []int64{1, 2, 3}, ids(page), "in part order")
	require.Len(t, page[0].Creators, 1, "with blurb relations")
	assert.Len(t, page[0].Series, 2)

	page, total, err = e.svc.ListSeriesWorks(ctx, 77, 1, 1)
	require.NoError(t, err)
	assert.Equal(t, 3, total, "over all pages")
	assert.Equal(t, []int64{2}, ids(page))

	v, err := e.svc.GetSeries(ctx, 77)
	require.NoError(t, err)
	assert.Equal(t, v.Works, total, "the series view's count")

	_, _, err = e.svc.ListSeriesWorks(ctx, 999, 0, 10)
	require.ErrorIs(t, err, store.ErrNoSeries)
	_, _, err = e.svc.ListSeriesWorks(ctx, 77, 0, store.MaxWorkBatch+1)
	require.Error(t, err)
}
