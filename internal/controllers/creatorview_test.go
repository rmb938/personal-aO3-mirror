package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// seedViewCreators stores works 1 and 2 by fixture_author (pseud fixture_pseud), work 2 also by
// fixture_b, and work 3 by fixture_b alone and complete; series 77's page names fixture_c (no
// works). It returns each creator's id by pseud.
func seedViewCreators(t *testing.T, e *env) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	a := model.Creator{User: "fixture_author", Pseud: "fixture_pseud"}
	b := model.Creator{User: "fixture_b", Pseud: "fixture_b"}
	require.NoError(t, e.st.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		for _, w := range []model.Work{
			{ID: 1, Title: "Fixture Title", Revised: "2026-11-01", Creators: []model.Creator{a}},
			{ID: 2, Title: "Fixture Title", Revised: "2026-11-02", Creators: []model.Creator{a, b}},
			{ID: 3, Title: "Fixture Title", Revised: "2026-11-03", Creators: []model.Creator{b}, Complete: true},
		} {
			if _, err := tx.UpsertWork(ctx, w, model.FromPage, 1); err != nil {
				return err
			}
		}
		return tx.UpsertSeries(ctx, model.Series{ID: 77, Title: "Fixture Series",
			Creators: []model.Creator{{User: "fixture_c", Pseud: "fixture_c"}}})
	}))
	page, _, err := e.svc.ListCreators(ctx, model.CreatorFilter{Limit: store.MaxCreatorPage})
	require.NoError(t, err)
	ids := map[string]int64{}
	for _, r := range page {
		ids[r.Pseud] = r.ID
	}
	require.Len(t, ids, 3)
	return ids
}

func TestGetCreator(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	ids := seedViewCreators(t, e)

	v, err := e.svc.GetCreator(ctx, ids["fixture_pseud"])
	require.NoError(t, err)
	assert.Equal(t, store.CreatorRef{ID: ids["fixture_pseud"], User: "fixture_author", Pseud: "fixture_pseud"}, v.Creator)
	assert.Equal(t, 2, v.Works)

	v, err = e.svc.GetCreator(ctx, ids["fixture_c"])
	require.NoError(t, err, "a creator named by a series page only")
	assert.Zero(t, v.Works)

	_, err = e.svc.GetCreator(ctx, 999)
	require.ErrorIs(t, err, store.ErrNoCreator)
}

func TestListCreators(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	seedViewCreators(t, e)
	pseuds := func(page []store.CreatorRow) []string {
		var out []string
		for _, r := range page {
			out = append(out, r.Pseud)
		}
		return out
	}

	page, total, err := e.svc.ListCreators(ctx, model.CreatorFilter{Sort: model.CreatorSortWorks, Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, 3, total, "every stored creator, over all pages")
	assert.Equal(t, []string{"fixture_b", "fixture_pseud"}, pseuds(page), "a works tie by name")
	assert.Equal(t, 2, page[0].Works)

	page, total, err = e.svc.ListCreators(ctx, model.CreatorFilter{Name: "fixture_", Offset: 1, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Equal(t, []string{"fixture_c", "fixture_pseud"}, pseuds(page))

	_, _, err = e.svc.ListCreators(ctx, model.CreatorFilter{Limit: store.MaxCreatorPage + 1})
	require.Error(t, err)
}

func TestListCreatorWorks(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	ids := seedViewCreators(t, e)
	workIDs := func(page []WorkBlurb) []int64 {
		var out []int64
		for _, b := range page {
			out = append(out, b.Work.ID)
		}
		return out
	}

	page, total, err := e.svc.ListCreatorWorks(ctx, ids["fixture_b"], model.WorkFilter{Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Equal(t, []int64{3, 2}, workIDs(page), "newest revised first")
	require.Len(t, page[1].Creators, 2, "with blurb relations")

	v, err := e.svc.GetCreator(ctx, ids["fixture_b"])
	require.NoError(t, err)
	assert.Equal(t, v.Works, total, "the creator view's count")

	yes := true
	page, total, err = e.svc.ListCreatorWorks(ctx, ids["fixture_b"], model.WorkFilter{Complete: &yes, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, []int64{3}, workIDs(page), "the filter's other fields apply")

	f := model.WorkFilter{CreatorID: ids["fixture_b"], Limit: 10}
	page, _, err = e.svc.ListCreatorWorks(ctx, ids["fixture_pseud"], f)
	require.NoError(t, err)
	assert.Equal(t, []int64{2, 1}, workIDs(page), "the path creator replaces the filter's")

	_, total, err = e.svc.ListCreatorWorks(ctx, ids["fixture_c"], model.WorkFilter{Limit: 10})
	require.NoError(t, err)
	assert.Zero(t, total)

	_, _, err = e.svc.ListCreatorWorks(ctx, 999, model.WorkFilter{Limit: 10})
	require.ErrorIs(t, err, store.ErrNoCreator)
}
