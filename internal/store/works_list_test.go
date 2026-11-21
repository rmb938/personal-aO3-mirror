package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// seedListWorks stores four synthetic works whose fields differ per filter:
//
//	id  origin  status   complete expected lang     words kudos hits comments bookmarks published  revised
//	1   page    ok       yes      1        English  500   50    1000 5        3         2025-01-01 2025-02-01
//	2   page    ok       no       3        English  2000  10    100  1        2         2025-06-01 2026-01-01
//	3   blurb   ok       no       0        Deutsch  2000  10    100  1        2         (none)     2026-01-01
//	4   page    deleted  no       3        English  100   0     10   0        0         2024-05-05 2024-05-05
//
// Works 2 and 3 tie on words, kudos and revised, so the id tie-breaker decides their order.
func seedListWorks(t *testing.T) *testStore {
	t.Helper()
	s, _, _ := openTest(t)
	ctx := context.Background()
	w := func(id int64, f func(*model.Work)) model.Work {
		x := fixtureWork(id)
		x.Series = nil
		f(&x)
		return x
	}
	works := []struct {
		w      model.Work
		origin model.Origin
	}{
		{w(1, func(x *model.Work) {
			x.Complete, x.ChaptersPosted, x.ChaptersExpected, x.Words = true, 1, 1, 500
			x.Stats = model.Stats{Kudos: 50, Hits: 1000, Comments: 5, Bookmarks: 3}
			x.Published, x.Revised = "2025-01-01", "2025-02-01"
		}), model.FromPage},
		{w(2, func(x *model.Work) {
			x.Words, x.Published, x.Revised = 2000, "2025-06-01", "2026-01-01"
		}), model.FromPage},
		{w(3, func(x *model.Work) {
			x.Language, x.ChaptersExpected, x.Words, x.Revised = "Deutsch", 0, 2000, "2026-01-01"
		}), model.FromBlurb},
		{w(4, func(x *model.Work) {
			x.Words, x.Published, x.Revised = 100, "2024-05-05", "2024-05-05"
			x.Stats = model.Stats{Hits: 10}
		}), model.FromPage},
	}
	for _, x := range works {
		_, err := s.UpsertWork(ctx, x.w, x.origin, 1)
		require.NoError(t, err)
	}
	require.NoError(t, s.SetWorkStatus(ctx, 4, model.StatusDeleted))
	return s
}

func TestFindWorks(t *testing.T) {
	s := seedListWorks(t)
	ctx := context.Background()
	day := func(d string) time.Time {
		v, err := time.Parse(time.DateOnly, d)
		require.NoError(t, err)
		return v.Add(15 * time.Hour) // the time of day is ignored
	}
	n := func(v int) *int { return &v }
	yes, no := new(true), new(false)
	for _, tc := range []struct {
		name string
		f    model.WorkFilter
		want []int64
	}{
		{"default: revised newest first", model.WorkFilter{}, []int64{3, 2, 1, 4}},
		{"revised asc", model.WorkFilter{Direction: model.SortAsc}, []int64{4, 1, 2, 3}},
		{"revised desc", model.WorkFilter{Sort: model.WorkSortRevised, Direction: model.SortDesc}, []int64{3, 2, 1, 4}},
		{"kudos", model.WorkFilter{Sort: model.WorkSortKudos}, []int64{1, 3, 2, 4}},
		{"kudos asc", model.WorkFilter{Sort: model.WorkSortKudos, Direction: model.SortAsc}, []int64{4, 2, 3, 1}},
		{"published: unknown last", model.WorkFilter{Sort: model.WorkSortPublished}, []int64{2, 1, 4, 3}},
		{"words", model.WorkFilter{Sort: model.WorkSortWords}, []int64{3, 2, 1, 4}},
		{"hits", model.WorkFilter{Sort: model.WorkSortHits}, []int64{1, 3, 2, 4}},
		{"comments", model.WorkFilter{Sort: model.WorkSortComments}, []int64{1, 3, 2, 4}},
		{"bookmarks asc", model.WorkFilter{Sort: model.WorkSortBookmarks, Direction: model.SortAsc}, []int64{4, 2, 3, 1}},
		{"complete", model.WorkFilter{Complete: yes}, []int64{1}},
		{"incomplete", model.WorkFilter{Complete: no}, []int64{3, 2, 4}},
		{"single chapter", model.WorkFilter{SingleChapter: true}, []int64{1}},
		{"language", model.WorkFilter{Language: "Deutsch"}, []int64{3}},
		{"status deleted", model.WorkFilter{Statuses: []model.WorkStatus{model.StatusDeleted}}, []int64{4}},
		{"status ok or hidden", model.WorkFilter{Statuses: []model.WorkStatus{model.StatusOK, model.StatusHidden}},
			[]int64{3, 2, 1}},
		{"fetched", model.WorkFilter{Fetched: yes}, []int64{2, 1, 4}},
		{"blurb only", model.WorkFilter{Fetched: no}, []int64{3}},
		{"words min", model.WorkFilter{Words: model.IntRange{Min: n(1000)}}, []int64{3, 2}},
		{"words max", model.WorkFilter{Words: model.IntRange{Max: n(500)}}, []int64{1, 4}},
		{"words exact", model.WorkFilter{Words: model.IntRange{Min: n(500), Max: n(500)}}, []int64{1}},
		{"kudos max 0", model.WorkFilter{Kudos: model.IntRange{Max: n(0)}}, []int64{4}},
		{"hits min", model.WorkFilter{Hits: model.IntRange{Min: n(101)}}, []int64{1}},
		{"comments min", model.WorkFilter{Comments: model.IntRange{Min: n(1)}}, []int64{3, 2, 1}},
		{"bookmarks range", model.WorkFilter{Bookmarks: model.IntRange{Min: n(1), Max: n(2)}}, []int64{3, 2}},
		{"revised range", model.WorkFilter{Revised: model.DateRange{From: day("2025-01-01"), To: day("2025-12-31")}},
			[]int64{1}},
		{"revised from (inclusive)", model.WorkFilter{Revised: model.DateRange{From: day("2026-01-01")}},
			[]int64{3, 2}},
		{"published to: unknown excluded", model.WorkFilter{Published: model.DateRange{To: day("2025-01-01")}},
			[]int64{1, 4}},
		{"combined", model.WorkFilter{Complete: no, Statuses: []model.WorkStatus{model.StatusOK},
			Words: model.IntRange{Min: n(1000)}, Sort: model.WorkSortWords, Direction: model.SortAsc}, []int64{2, 3}},
		{"none match", model.WorkFilter{Language: "Fixture Language"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.f
			f.Limit = 10
			page, err := s.FindWorks(ctx, f)
			require.NoError(t, err)
			require.Equal(t, tc.want, workIDs(page))
			total, err := s.CountWorks(ctx, f)
			require.NoError(t, err)
			require.Equal(t, len(tc.want), total)
		})
	}
}

func workIDs(rows []WorkRow) []int64 {
	var ids []int64
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestFindWorksPagesAndRows(t *testing.T) {
	s := seedListWorks(t)
	ctx := context.Background()
	page, err := s.FindWorks(ctx, model.WorkFilter{Offset: 1, Limit: 2})
	require.NoError(t, err)
	require.Equal(t, []int64{2, 1}, workIDs(page))
	page, err = s.FindWorks(ctx, model.WorkFilter{Offset: 4, Limit: 2})
	require.NoError(t, err)
	require.Empty(t, page)
	total, err := s.CountWorks(ctx, model.WorkFilter{Offset: 4, Limit: 2})
	require.NoError(t, err)
	require.Equal(t, 4, total, "count ignores the page")

	// Listing rows are the same as Work's.
	page, err = s.FindWorks(ctx, model.WorkFilter{Limit: 10})
	require.NoError(t, err)
	for _, got := range page {
		want, err := s.Work(ctx, got.ID)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

func TestFindWorksRejectsBadFilters(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	for name, f := range map[string]model.WorkFilter{
		"negative offset": {Offset: -1, Limit: 1},
		"zero limit":      {},
		"sort":            {Sort: "title", Limit: 1},
		"direction":       {Direction: "up", Limit: 1},
	} {
		_, err := s.FindWorks(ctx, f)
		require.Error(t, err, name)
	}
	// Bad filters fail the count too.
	for name, f := range map[string]model.WorkFilter{
		"status":   {Statuses: []model.WorkStatus{"gone"}, Limit: 1},
		"too many": {Statuses: append(model.WorkStatuses(), model.StatusOK), Limit: 1},
	} {
		_, err := s.FindWorks(ctx, f)
		require.Error(t, err, name)
		_, err = s.CountWorks(ctx, f)
		require.Error(t, err, name)
	}
}

// listPlanFilters are filters on columns with no index of their own (plus status, kept off its
// index): a page must walk the sort index rather than sort every match.
var listPlanFilters = []model.WorkFilter{
	{},
	{Statuses: []model.WorkStatus{model.StatusOK}},
	{Statuses: []model.WorkStatus{model.StatusOK}, Complete: new(true), Language: "English", Fetched: new(true),
		SingleChapter: true},
}

func TestWorksPagePlans(t *testing.T) {
	s, _, _ := openTest(t)
	for _, sort := range model.WorkSorts() {
		for _, dir := range []model.SortDirection{model.SortDesc, model.SortAsc} {
			for i, f := range listPlanFilters {
				f.Sort, f.Direction = sort, dir
				where, args, err := workWhere(f)
				require.NoError(t, err)
				order, err := workOrder(f)
				require.NoError(t, err)
				plan := explain(t, s, `SELECT id FROM works`+where+order+` LIMIT 1 OFFSET 1`, args...)
				require.NotContains(t, plan, "TEMP B-TREE", "%s %s filter %d: %s", sort, dir, i, plan)
				require.Contains(t, plan, "INDEX works_", "%s %s filter %d: %s", sort, dir, i, plan)
			}
		}
	}
}

// Benchmarks for listing works over a large synthetic table. No real data.
//
//	go test ./internal/store -run '^$' -bench 'Works' -benchtime 20x

const benchWorks = 200_000

// seedWorks inserts benchWorks synthetic works; each field comes from different digits of the
// id so filters are not correlated: ~95% ok, ~40% complete, ~10% single chapter, ~80% English,
// ~70% fetched, and spread words, stats and dates (many ties).
func seedWorks(b *testing.B) *testStore {
	b.Helper()
	s, _, _ := openTest(b)
	_, err := s.meta.ExecContext(context.Background(), `
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO works (id, title, language, words, chapters_posted, chapters_expected, complete,
			published, revised_at, kudos, hits, comments, bookmarks, status, first_seen, last_fetched)
		SELECT i, 'Fixture Title ' || i,
			CASE WHEN i % 5 = 0 THEN 'Deutsch' ELSE 'English' END,
			(i * 7919) % 200000, 1, CASE WHEN (i / 3) % 10 = 0 THEN 1 ELSE 3 END, (i / 7) % 5 < 2,
			CASE WHEN (i / 11) % 10 < 3 THEN '' ELSE date('2010-01-01', '+' || ((i * 31) % 5000) || ' days') END,
			date('2010-01-01', '+' || ((i * 37) % 6000) || ' days'),
			(i * 104729) % 5000, (i * 1299709) % 100000, (i * 13) % 200, (i * 17) % 500,
			CASE WHEN (i / 13) % 20 = 0 THEN 'deleted' ELSE 'ok' END, 1,
			CASE WHEN (i / 11) % 10 < 3 THEN 0 ELSE i END
		FROM n`, benchWorks)
	require.NoError(b, err)
	return s
}

var benchWorkFilters = map[string]model.WorkFilter{
	"none": {},
	"ok":   {Statuses: []model.WorkStatus{model.StatusOK}},
	"ok,complete,english": {Statuses: []model.WorkStatus{model.StatusOK}, Complete: new(true),
		Language: "English"},
	"single,fetched": {SingleChapter: true, Fetched: new(true)},
	"words>=100k":    {Words: model.IntRange{Min: new(100_000)}},
	"revised 2020":   {Revised: model.DateRange{From: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2020, 12, 31, 0, 0, 0, 0, time.UTC)}},
}

func BenchmarkWorksPage(b *testing.B) {
	s := seedWorks(b)
	ctx := context.Background()
	for name, f := range benchWorkFilters {
		for _, sort := range []model.WorkSort{model.WorkSortRevised, model.WorkSortKudos} {
			for _, offset := range []int{0, 100_000 - 100} {
				f := f
				f.Sort, f.Offset, f.Limit = sort, offset, 100
				b.Run(fmt.Sprintf("%s/%s/offset=%d", name, sort, offset), func(b *testing.B) {
					for b.Loop() {
						_, err := s.FindWorks(ctx, f)
						require.NoError(b, err)
					}
				})
			}
		}
	}
}

func BenchmarkWorksCount(b *testing.B) {
	s := seedWorks(b)
	ctx := context.Background()
	for name, f := range benchWorkFilters {
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				_, err := s.CountWorks(ctx, f)
				require.NoError(b, err)
			}
		})
	}
}
