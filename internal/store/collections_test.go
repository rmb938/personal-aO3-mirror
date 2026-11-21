package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// seedCollections stores work 1 in fixture_coll_a and fixture_coll_b, work 2 in fixture_coll_a
// and work 3 in fixture_coll_c, then moves work 3 out of it (its row stays, with no works). It
// returns each collection's id by name.
func seedCollections(t *testing.T) (*testStore, map[string]int64) {
	t.Helper()
	s, _, _ := openTest(t)
	ctx := context.Background()
	a := model.Collection{Name: "fixture_coll_a", Title: "Fixture Collection A"}
	b := model.Collection{Name: "fixture_coll_b", Title: "Fixture Collection B"}
	c := model.Collection{Name: "fixture_coll_c", Title: "Fixture Collection C"}
	for _, step := range []struct {
		id int64
		cs []model.Collection
	}{{1, []model.Collection{a, b}}, {2, []model.Collection{a}}, {3, []model.Collection{c}}, {3, nil}} {
		w := fixtureWork(step.id)
		w.Collections = step.cs
		_, err := s.UpsertWork(ctx, w, model.FromPage, 1)
		require.NoError(t, err)
	}
	ids := map[string]int64{}
	rows, err := s.meta.QueryContext(ctx, `SELECT id, name FROM collections`)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	for rows.Next() {
		var id int64
		var name string
		require.NoError(t, rows.Scan(&id, &name))
		ids[name] = id
	}
	require.NoError(t, rows.Err())
	require.Len(t, ids, 3)
	return s, ids
}

func TestCollectionByID(t *testing.T) {
	s, ids := seedCollections(t)
	ctx := context.Background()
	c, err := s.CollectionByID(ctx, ids["fixture_coll_a"])
	require.NoError(t, err)
	require.Equal(t, CollectionRef{ID: ids["fixture_coll_a"], Name: "fixture_coll_a", Title: "Fixture Collection A"}, c)
	_, err = s.CollectionByID(ctx, 999)
	require.ErrorIs(t, err, ErrNoCollection)
}

func TestFindWorksByCollection(t *testing.T) {
	s, ids := seedCollections(t)
	ctx := context.Background()
	for name, want := range map[string][]int64{
		"fixture_coll_a": {1, 2}, "fixture_coll_b": {1}, "fixture_coll_c": nil,
	} {
		rows, err := s.FindWorks(ctx, model.WorkFilter{CollectionID: ids[name], Sort: model.WorkSortTitle, Limit: 10})
		require.NoError(t, err)
		var got []int64
		for _, r := range rows {
			got = append(got, r.ID)
		}
		require.Equal(t, want, got, name)
		n, err := s.CountWorks(ctx, model.WorkFilter{CollectionID: ids[name]})
		require.NoError(t, err)
		require.Equal(t, len(want), n, "%s count", name)
	}
	n, err := s.CountWorks(ctx, model.WorkFilter{CollectionID: ids["fixture_coll_a"], Tags: []int64{999}})
	require.NoError(t, err)
	require.Zero(t, n, "combines with the other filters")
}

// BenchmarkCollectionWorks times a collection's works over seedWorks' works, work i in
// collection i % 1000 (+1) and every 10th work in a second one:
//
//	go test ./internal/store -run '^$' -bench 'CollectionWorks' -benchtime 20x
func BenchmarkCollectionWorks(b *testing.B) {
	s := seedWorks(b)
	ctx := context.Background()
	const collections = 1000
	_, err := s.meta.ExecContext(ctx, fmt.Sprintf(`
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < %[1]d)
		INSERT INTO collections (id, name, title) SELECT i, 'fixture_coll_' || i, 'Fixture Collection ' || i FROM n;
		INSERT INTO work_collections (work_id, collection_id) SELECT id, id %% %[1]d + 1 FROM works;
		INSERT OR IGNORE INTO work_collections (work_id, collection_id)
			SELECT id, (id * 7) %% %[1]d + 1 FROM works WHERE id %% 10 = 0;`, collections))
	require.NoError(b, err)
	works := model.WorkFilter{CollectionID: 7, Limit: MaxWorkBatch}
	b.Run("page", func(b *testing.B) {
		for b.Loop() {
			_, err := s.FindWorks(ctx, works)
			require.NoError(b, err)
		}
	})
	b.Run("count", func(b *testing.B) {
		for b.Loop() {
			_, err := s.CountWorks(ctx, works)
			require.NoError(b, err)
		}
	})
}

func TestCollectionWorksPlan(t *testing.T) {
	s, _, _ := openTest(t)
	where, args, err := workWhere(model.WorkFilter{CollectionID: 1})
	require.NoError(t, err)
	plan := explain(t, s, `SELECT COUNT(*) FROM works`+where, args...)
	require.Contains(t, plan, "USING COVERING INDEX work_collections_collection_work (collection_id=?)", plan)
}
