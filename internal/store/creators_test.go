package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// seedCreators stores work 1 by fixture_author/fixture_pseud and fixture_b, work 2 by
// fixture_pseud alone and work 3 by Fixture_C; series 77's page names fixture_d (no works). It
// returns each creator's id by pseud.
func seedCreators(t *testing.T) (*testStore, map[string]int64) {
	t.Helper()
	s, _, _ := openTest(t)
	ctx := context.Background()
	for id, cs := range map[int64][]model.Creator{
		1: {{User: "fixture_author", Pseud: "fixture_pseud"}, {User: "fixture_b", Pseud: "fixture_b"}},
		2: {{User: "fixture_author", Pseud: "fixture_pseud"}},
		3: {{User: "fixture_c", Pseud: "Fixture_C"}},
	} {
		w := fixtureWork(id)
		w.Creators = cs
		_, err := s.UpsertWork(ctx, w, model.FromPage, 1)
		require.NoError(t, err)
	}
	require.NoError(t, s.UpsertSeries(ctx, model.Series{ID: 77, Creators: []model.Creator{{User: "fixture_d", Pseud: "fixture_d"}}}))
	rows, err := s.FindCreators(ctx, model.CreatorFilter{Limit: MaxCreatorPage})
	require.NoError(t, err)
	ids := map[string]int64{}
	for _, r := range rows {
		ids[r.Pseud] = r.ID
	}
	require.Len(t, ids, 4)
	return s, ids
}

func creatorPseuds(rows []CreatorRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Pseud)
	}
	return out
}

func TestCreatorByID(t *testing.T) {
	s, ids := seedCreators(t)
	ctx := context.Background()
	c, err := s.CreatorByID(ctx, ids["fixture_pseud"])
	require.NoError(t, err)
	require.Equal(t, CreatorRef{ID: ids["fixture_pseud"], User: "fixture_author", Pseud: "fixture_pseud"}, c)
	_, err = s.CreatorByID(ctx, 999)
	require.ErrorIs(t, err, ErrNoCreator)
}

func TestFindCreators(t *testing.T) {
	s, _ := seedCreators(t)
	ctx := context.Background()
	byName := []string{"fixture_b", "Fixture_C", "fixture_d", "fixture_pseud"}
	for _, tc := range []struct {
		name string
		f    model.CreatorFilter
		want []string
	}{
		{"by pseud ignoring case by default", model.CreatorFilter{}, byName},
		{"by name descending", model.CreatorFilter{Direction: model.SortDesc},
			[]string{"fixture_pseud", "fixture_d", "Fixture_C", "fixture_b"}},
		{"by works, most first, ties by name", model.CreatorFilter{Sort: model.CreatorSortWorks},
			[]string{"fixture_pseud", "fixture_b", "Fixture_C", "fixture_d"}},
		{"by works ascending, ties still by name", model.CreatorFilter{Sort: model.CreatorSortWorks, Direction: model.SortAsc},
			[]string{"fixture_d", "fixture_b", "Fixture_C", "fixture_pseud"}},
		{"a word matches the pseud, ignoring case", model.CreatorFilter{Name: "PSEUD"}, []string{"fixture_pseud"}},
		{"a word matches the login", model.CreatorFilter{Name: "author"}, []string{"fixture_pseud"}},
		{"words all match, pseud or login", model.CreatorFilter{Name: "author  pseud"}, []string{"fixture_pseud"}},
		{"words all match or none", model.CreatorFilter{Name: "author c"}, nil},
		{"LIKE wildcards match themselves", model.CreatorFilter{Name: "%"}, nil},
		{"LIKE single-char wildcard matches itself", model.CreatorFilter{Name: "_c"}, []string{"Fixture_C"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.f
			f.Limit = MaxCreatorPage
			got, err := s.FindCreators(ctx, f)
			require.NoError(t, err)
			require.Equal(t, tc.want, creatorPseuds(got))
			n, err := s.CountCreators(ctx, f)
			require.NoError(t, err)
			require.Equal(t, len(tc.want), n, "count")
		})
	}

	t.Run("pages", func(t *testing.T) {
		for _, sort := range model.CreatorSorts() {
			var all []string
			for off := 0; off < len(byName); off += 3 {
				got, err := s.FindCreators(ctx, model.CreatorFilter{Sort: sort, Offset: off, Limit: 3})
				require.NoError(t, err)
				all = append(all, creatorPseuds(got)...)
			}
			require.ElementsMatch(t, byName, all, "every creator once, sort %s", sort)
		}
	})

	t.Run("row fields and works counts", func(t *testing.T) {
		got, err := s.FindCreators(ctx, model.CreatorFilter{Limit: MaxCreatorPage})
		require.NoError(t, err)
		for _, r := range got {
			n, err := s.CountWorks(ctx, model.WorkFilter{CreatorID: r.ID})
			require.NoError(t, err)
			require.Equal(t, n, r.Works, "%s: the works filter's count", r.Pseud)
		}
		require.Equal(t, CreatorRow{CreatorRef{ID: got[3].ID, User: "fixture_author", Pseud: "fixture_pseud"}, 2}, got[3])
		require.Zero(t, got[2].Works, "named by a series page only")
	})
}

func TestFindWorksByCreator(t *testing.T) {
	s, ids := seedCreators(t)
	ctx := context.Background()
	for pseud, want := range map[string][]int64{
		"fixture_pseud": {1, 2}, "fixture_b": {1}, "Fixture_C": {3}, "fixture_d": nil,
	} {
		rows, err := s.FindWorks(ctx, model.WorkFilter{CreatorID: ids[pseud], Sort: model.WorkSortTitle, Limit: 10})
		require.NoError(t, err)
		var got []int64
		for _, r := range rows {
			got = append(got, r.ID)
		}
		require.Equal(t, want, got, pseud)
	}
	n, err := s.CountWorks(ctx, model.WorkFilter{CreatorID: ids["fixture_pseud"], Tags: []int64{999}})
	require.NoError(t, err)
	require.Zero(t, n, "combines with the other filters")
}

func TestFindCreatorsRejects(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	words := ""
	for range model.MaxCreatorNameWords + 1 {
		words += "w "
	}
	for name, f := range map[string]model.CreatorFilter{
		"negative offset": {Offset: -1, Limit: 1},
		"zero limit":      {},
		"limit over page": {Limit: MaxCreatorPage + 1},
		"too many words":  {Name: words, Limit: 1},
		"only spaces":     {Name: "  ", Limit: 1},
		"unknown sort":    {Sort: "created", Limit: 1},
		"unknown dir":     {Direction: "up", Limit: 1},
	} {
		_, err := s.FindCreators(ctx, f)
		require.Error(t, err, name)
	}
	_, err := s.CountCreators(ctx, model.CreatorFilter{Name: "  "})
	require.Error(t, err)
}

// seedCreatorBench adds benchCreators creators to seedWorks' works, creator i % benchCreators
// (+1) on work i, and a second byline creator on every 10th work.
func seedCreatorBench(b *testing.B) *testStore {
	b.Helper()
	s := seedWorks(b)
	_, err := s.meta.ExecContext(context.Background(), fmt.Sprintf(`
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < %[1]d)
		INSERT INTO creators (id, user_login, pseud) SELECT i, 'fixture_user_' || i, 'Fixture Pseud ' || i FROM n;
		INSERT INTO work_creators (work_id, creator_id, position) SELECT id, id %% %[1]d + 1, 0 FROM works;
		INSERT OR IGNORE INTO work_creators (work_id, creator_id, position)
			SELECT id, (id * 7) %% %[1]d + 1, 1 FROM works WHERE id %% 10 = 0;`, benchCreators))
	require.NoError(b, err)
	return s
}

const benchCreators = 50_000

// BenchmarkCreators times creator pages and counts and a creator's works over seedWorks'
// works with 50k creators:
//
//	go test ./internal/store -run '^$' -bench 'Creators' -benchtime 20x
func BenchmarkCreators(b *testing.B) {
	s := seedCreatorBench(b)
	ctx := context.Background()
	for name, f := range map[string]model.CreatorFilter{"none": {}, "name word": {Name: "pseud 1"}} {
		for _, sort := range model.CreatorSorts() {
			for _, offset := range []int{0, 1000} {
				f := f
				f.Sort, f.Offset, f.Limit = sort, offset, MaxCreatorPage
				b.Run(fmt.Sprintf("page/%s/%s/offset=%d", name, sort, offset), func(b *testing.B) {
					for b.Loop() {
						_, err := s.FindCreators(ctx, f)
						require.NoError(b, err)
					}
				})
			}
		}
		b.Run("count/"+name, func(b *testing.B) {
			for b.Loop() {
				_, err := s.CountCreators(ctx, f)
				require.NoError(b, err)
			}
		})
	}
	works := model.WorkFilter{CreatorID: 7, Limit: MaxWorkBatch}
	b.Run("works/page", func(b *testing.B) {
		for b.Loop() {
			_, err := s.FindWorks(ctx, works)
			require.NoError(b, err)
		}
	})
	b.Run("works/count", func(b *testing.B) {
		for b.Loop() {
			_, err := s.CountWorks(ctx, works)
			require.NoError(b, err)
		}
	})
}

func TestCreatorListPlans(t *testing.T) {
	s, _, _ := openTest(t)
	for name, tc := range map[string]struct {
		f    model.CreatorFilter
		want []string
	}{
		"by name walks the name index": {model.CreatorFilter{},
			[]string{"SCAN c USING COVERING INDEX creators_name", "work_creators_creator_work (creator_id=?)"}},
		"by works counts over the covering index": {model.CreatorFilter{Sort: model.CreatorSortWorks},
			[]string{"USING COVERING INDEX work_creators_creator_work (creator_id=?)"}},
	} {
		q, args, err := creatorPageQuery(tc.f)
		require.NoError(t, err)
		plan := explain(t, s, q, append(args, 10, 0)...)
		for _, w := range tc.want {
			require.Contains(t, plan, w, "%s:\n%s", name, plan)
		}
		require.NotContains(t, plan, "SCAN wc", "%s:\n%s", name, plan)
	}

	where, args, err := workWhere(model.WorkFilter{CreatorID: 1})
	require.NoError(t, err)
	plan := explain(t, s, `SELECT COUNT(*) FROM works`+where, args...)
	require.Contains(t, plan, "USING COVERING INDEX work_creators_creator_work (creator_id=?)", plan)
}
