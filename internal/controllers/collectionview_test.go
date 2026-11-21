package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// seedViewCollections stores works 1 and 2 in fixture_coll_a, work 2 also in fixture_coll_b,
// and work 3 in fixture_coll_b alone and complete; work 4 was in fixture_coll_c and left it. It
// returns each collection's id by name.
func seedViewCollections(t *testing.T, e *env) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	a := model.Collection{Name: "fixture_coll_a", Title: "Fixture Collection A"}
	b := model.Collection{Name: "fixture_coll_b", Title: "Fixture Collection B"}
	c := model.Collection{Name: "fixture_coll_c", Title: "Fixture Collection C"}
	upsert := func(ws ...model.Work) {
		require.NoError(t, e.st.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
			for _, w := range ws {
				if _, err := tx.UpsertWork(ctx, w, model.FromPage, 1); err != nil {
					return err
				}
			}
			return nil
		}))
	}
	upsert(
		model.Work{ID: 1, Title: "Fixture Title", Revised: "2026-11-01", Collections: []model.Collection{a}},
		model.Work{ID: 2, Title: "Fixture Title", Revised: "2026-11-02", Collections: []model.Collection{a, b}},
		model.Work{ID: 3, Title: "Fixture Title", Revised: "2026-11-03", Collections: []model.Collection{b}, Complete: true},
		model.Work{ID: 4, Title: "Fixture Title", Revised: "2026-11-04", Collections: []model.Collection{c}},
	)
	ids := map[string]int64{}
	for _, id := range []int64{2, 4} {
		v, err := e.svc.GetWork(ctx, id)
		require.NoError(t, err)
		for _, c := range v.Collections {
			ids[c.Name] = c.ID
		}
	}
	require.Len(t, ids, 3)
	upsert(model.Work{ID: 4, Title: "Fixture Title", Revised: "2026-11-04"})
	return ids
}

func TestGetCollection(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	ids := seedViewCollections(t, e)

	v, err := e.svc.GetCollection(ctx, ids["fixture_coll_a"])
	require.NoError(t, err)
	assert.Equal(t, store.CollectionRef{ID: ids["fixture_coll_a"], Name: "fixture_coll_a", Title: "Fixture Collection A"}, v.Collection)
	assert.Equal(t, 2, v.Works)

	v, err = e.svc.GetCollection(ctx, ids["fixture_coll_c"])
	require.NoError(t, err, "a collection its only work left")
	assert.Equal(t, "fixture_coll_c", v.Collection.Name)
	assert.Zero(t, v.Works)

	_, err = e.svc.GetCollection(ctx, 999)
	require.ErrorIs(t, err, store.ErrNoCollection)
}

func TestListCollectionWorks(t *testing.T) {
	e := newEnv(t, 0)
	ctx := context.Background()
	ids := seedViewCollections(t, e)
	workIDs := func(page []WorkBlurb) []int64 {
		var out []int64
		for _, b := range page {
			out = append(out, b.Work.ID)
		}
		return out
	}

	page, total, err := e.svc.ListCollectionWorks(ctx, ids["fixture_coll_b"], model.WorkFilter{Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Equal(t, []int64{3, 2}, workIDs(page), "newest revised first")

	v, err := e.svc.GetCollection(ctx, ids["fixture_coll_b"])
	require.NoError(t, err)
	assert.Equal(t, v.Works, total, "the collection view's count")

	yes := true
	page, total, err = e.svc.ListCollectionWorks(ctx, ids["fixture_coll_b"], model.WorkFilter{Complete: &yes, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, []int64{3}, workIDs(page), "the filter's other fields apply")

	f := model.WorkFilter{CollectionID: ids["fixture_coll_b"], Limit: 10}
	page, _, err = e.svc.ListCollectionWorks(ctx, ids["fixture_coll_a"], f)
	require.NoError(t, err)
	assert.Equal(t, []int64{2, 1}, workIDs(page), "the path collection replaces the filter's")

	_, total, err = e.svc.ListCollectionWorks(ctx, ids["fixture_coll_c"], model.WorkFilter{Limit: 10})
	require.NoError(t, err)
	assert.Zero(t, total)

	_, _, err = e.svc.ListCollectionWorks(ctx, 999, model.WorkFilter{Limit: 10})
	require.ErrorIs(t, err, store.ErrNoCollection)
}
