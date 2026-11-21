package store

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// crossoverGraph is a synthetic fandom graph (every tag a Fandom):
//
//	M1 ← meta of A, B, H;  M2 ← meta of C, H
//	Top ← meta of Mid ← meta of D;  Top ← meta of E
//	S → synonym of A (listed on A's page);  X Alias → synonym of X (listed on its own page)
//	X, Y: no meta tags
var crossoverGraph = []struct {
	name string
	u    TagInfoUpdate
}{
	{"A", TagInfoUpdate{MetaTags: []string{"M1"}, Synonyms: []string{"S"}}},
	{"B", TagInfoUpdate{MetaTags: []string{"M1"}}},
	{"C", TagInfoUpdate{MetaTags: []string{"M2"}}},
	{"H", TagInfoUpdate{MetaTags: []string{"M1", "M2"}}},
	{"Mid", TagInfoUpdate{MetaTags: []string{"Top"}}},
	{"D", TagInfoUpdate{MetaTags: []string{"Mid"}}},
	{"E", TagInfoUpdate{MetaTags: []string{"Top"}}},
	{"X Alias", TagInfoUpdate{MergerOf: "X"}},
}

// crossoverWorks are the works' fandoms and whether each is a crossover by AO3's rule.
var crossoverWorks = []struct {
	id        int64
	fandoms   []string
	crossover bool
}{
	{1, []string{"A"}, false},           // one fandom
	{2, []string{"A", "X"}, true},       // unrelated
	{3, []string{"A", "B"}, false},      // share M1
	{4, []string{"A", "M1"}, false},     // a fandom and its meta tag
	{5, []string{"A", "S"}, false},      // a fandom and its synonym (listed on the canonical)
	{6, []string{"B", "C", "H"}, false}, // B and C are unrelated, but H is related to both
	{7, []string{"A", "B", "X"}, true},  // X is related to neither
	{8, []string{"D", "E"}, false},      // share Top, two levels above D
	{9, nil, false},                     // no fandom
	{10, []string{"X", "Y"}, true},
	{11, []string{"X", "X Alias"}, false}, // a fandom and its synonym (merger set on the synonym)
}

func fixtureFandom(name string) string { return "Fixture Fandom " + name }

func crossoverWork(id int64, fandoms []string) model.Work {
	w := fixtureWork(id)
	w.Series = nil
	w.Tags = []model.Tag{{Type: model.TagFreeform, Name: "fixture freeform"}}
	for _, f := range fandoms {
		w.Tags = append(w.Tags, model.Tag{Type: model.TagFandom, Name: fixtureFandom(f)})
	}
	w.Revised = fmt.Sprintf("2025-01-%02d", id) // newest first = highest id first
	return w
}

func applyTagInfo(t *testing.T, s *testStore, name string, u TagInfoUpdate) {
	t.Helper()
	ctx := context.Background()
	u.Type, u.Canonical = model.TagFandom, u.MergerOf == ""
	u.MergerOf = prefixFandom(u.MergerOf)
	for _, l := range []*[]string{&u.MetaTags, &u.Synonyms} {
		for i, n := range *l {
			(*l)[i] = fixtureFandom(n)
		}
	}
	id, err := s.tagID(ctx, s.meta, model.Tag{Type: model.TagFandom, Name: fixtureFandom(name)})
	require.NoError(t, err)
	require.NoError(t, s.UpdateTagInfo(ctx, id, u))
}

func prefixFandom(name string) string {
	if name == "" {
		return ""
	}
	return fixtureFandom(name)
}

func crossoverIDs(t *testing.T, s *testStore, crossover bool) []int64 {
	t.Helper()
	ctx := context.Background()
	f := model.WorkFilter{Crossover: &crossover, Limit: 100}
	page, err := s.FindWorks(ctx, f)
	require.NoError(t, err)
	n, err := s.CountWorks(ctx, f)
	require.NoError(t, err)
	require.Len(t, page, n)
	return workIDs(page)
}

func wantCrossover(crossover bool) []int64 {
	var ids []int64
	for _, w := range slices.Backward(crossoverWorks) {
		if w.crossover == crossover {
			ids = append(ids, w.id)
		}
	}
	return ids
}

// The same result whichever comes first: works stored before the graph (recomputed by each tag
// info update) or the graph before the works (computed on upsert).
func TestWorkCrossover(t *testing.T) {
	for _, graphFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("graph_first=%v", graphFirst), func(t *testing.T) {
			s, _, _ := openTest(t)
			ctx := context.Background()
			graph := func() {
				for _, g := range crossoverGraph {
					// Each case gets its own copy: applyTagInfo rewrites the name lists.
					u := g.u
					u.MetaTags, u.Synonyms = append([]string(nil), u.MetaTags...), append([]string(nil), u.Synonyms...)
					applyTagInfo(t, s, g.name, u)
				}
			}
			if graphFirst {
				graph()
			}
			for _, w := range crossoverWorks {
				_, err := s.UpsertWork(ctx, crossoverWork(w.id, w.fandoms), model.FromBlurb, 1)
				require.NoError(t, err)
			}
			if !graphFirst {
				graph()
			}
			require.Equal(t, wantCrossover(true), crossoverIDs(t, s, true))
			require.Equal(t, wantCrossover(false), crossoverIDs(t, s, false))
		})
	}
}

// A tag info update that changes a fandom's links recomputes the works under it; one that
// changes nothing (most refetches), or touches a non-fandom tag, writes no crossover.
func TestTagInfoCrossoverRecompute(t *testing.T) {
	s, _, logs := openTest(t)
	ctx := context.Background()
	for _, w := range []struct {
		id      int64
		fandoms []string
	}{{1, []string{"A", "B"}}, {2, []string{"Mid", "B"}}} {
		_, err := s.UpsertWork(ctx, crossoverWork(w.id, w.fandoms), model.FromBlurb, 1)
		require.NoError(t, err)
	}
	require.Equal(t, []int64{2, 1}, crossoverIDs(t, s, true))

	applyTagInfo(t, s, "A", TagInfoUpdate{MetaTags: []string{"M1"}})
	applyTagInfo(t, s, "B", TagInfoUpdate{MetaTags: []string{"M1"}})
	require.Equal(t, []int64{2}, crossoverIDs(t, s, true), "A and B now share M1")

	// Mid's sub D gains B's meta tag M1 through Mid: the update to Mid reaches work 2.
	applyTagInfo(t, s, "Mid", TagInfoUpdate{MetaTags: []string{"M1"}})
	require.Empty(t, crossoverIDs(t, s, true))

	// A stale value shows whether an update recomputed: an unchanged refetch leaves it alone.
	_, err := s.meta.ExecContext(ctx, `UPDATE works SET crossover = 1 WHERE id = 1`)
	require.NoError(t, err)
	logs.Reset()
	applyTagInfo(t, s, "A", TagInfoUpdate{MetaTags: []string{"M1"}})
	require.Equal(t, []int64{1}, crossoverIDs(t, s, true), "unchanged links: not recomputed")
	require.Contains(t, logs.String(), `"links_changed":false`)

	free, err := s.tagID(ctx, s.meta, model.Tag{Type: model.TagFreeform, Name: "fixture freeform"})
	require.NoError(t, err)
	require.NoError(t, s.UpdateTagInfo(ctx, free, TagInfoUpdate{Type: model.TagFreeform, Canonical: true,
		MetaTags: []string{"fixture freeform meta"}}))
	require.Equal(t, []int64{1}, crossoverIDs(t, s, true), "a freeform's links: not recomputed")

	// Dropping A's meta tag recomputes (and fixes the stale row); A and B are unrelated again.
	applyTagInfo(t, s, "A", TagInfoUpdate{})
	require.Equal(t, []int64{1}, crossoverIDs(t, s, true))
	_, err = s.meta.ExecContext(ctx, `UPDATE works SET crossover = 0 WHERE id = 1`)
	require.NoError(t, err)
	applyTagInfo(t, s, "A", TagInfoUpdate{MetaTags: []string{"M2"}})
	require.Equal(t, []int64{1}, crossoverIDs(t, s, true), "changed links: recomputed")
}

// 0006 adds works.crossover and backfills it (its Go step).
func TestMigrateWorkCrossover(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	old := openAt(t, dir, 5)
	_, err := old.meta.ExecContext(ctx, `
		INSERT INTO tags (id, name, type, merger_id, first_seen) VALUES
			(1, 'Fixture Fandom A', 'Fandom', NULL, 1), (2, 'Fixture Fandom B', 'Fandom', NULL, 1),
			(3, 'Fixture Fandom M', 'Fandom', NULL, 1), (4, 'Fixture Fandom X', 'Fandom', NULL, 1),
			(5, 'Fixture Fandom A Syn', 'Fandom', 1, 1), (6, 'fixture freeform', 'Freeform', NULL, 1);
		INSERT INTO tag_meta (sub_id, meta_id) VALUES (1, 3), (2, 3);
		INSERT INTO works (id, first_seen) VALUES (1, 1), (2, 1), (3, 1), (4, 1), (5, 1);
		INSERT INTO work_tags (work_id, tag_id, position) VALUES
			(1, 1, 0), (1, 2, 1), (2, 1, 0), (2, 4, 1), (3, 5, 0), (3, 1, 1), (4, 6, 0), (4, 4, 1),
			(5, 5, 0), (5, 4, 1);`)
	require.NoError(t, err)
	require.NoError(t, old.meta.Close())

	s, err := Open(ctx, dir, Options{})
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	got, err := queryInt64s(ctx, s.meta, `SELECT id FROM works WHERE crossover ORDER BY id`)
	require.NoError(t, err)
	require.Equal(t, []int64{2, 5}, got)
	n, err := updateCrossover(ctx, s.meta, `SELECT id FROM works`)
	require.NoError(t, err)
	require.Zero(t, n, "nothing left to recompute")
}

// BenchmarkCrossover times recomputing crossover over seedWorks with its tags 1–299 made fandoms
// (filler tags stay freeforms), so works have zero to two fandoms: every work (the 0006
// backfill), the works under a tag (a tag info change) and one work (an upsert). Rows are written
// only on the first run; later runs time the computation.
//
//	go test ./internal/store -run '^$' -bench 'Crossover' -benchtime 20x
func BenchmarkCrossover(b *testing.B) {
	s := seedWorks(b)
	ctx := context.Background()
	_, err := s.meta.ExecContext(ctx, `UPDATE tags SET type = 'Fandom' WHERE id < 1000`)
	require.NoError(b, err)
	for _, bc := range []struct {
		name string
		sel  string
		args []any
	}{
		{"all", `SELECT id FROM works`, nil},
		{"tag popular", tagFilterWorks(1), []any{1}},
		{"tag meta", tagFilterWorks(1), []any{3}},
		{"tag rare", tagFilterWorks(1), []any{2}},
		{"work", `VALUES (?)`, []any{3000}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			for b.Loop() {
				_, err := updateCrossover(ctx, s.meta, bc.sel, bc.args...)
				require.NoError(b, err)
			}
		})
	}
}
