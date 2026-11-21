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

// TestFindWorksTags checks AO3 filter_ids matching over a synthetic tag graph:
//
//	Fixture Top ← meta of Fixture Mid ← meta of Fixture Leaf
//	Fixture Top Syn → synonym of Top; Fixture Leaf Syn → synonym of Leaf
//	Fixture Cyc A ↔ Fixture Cyc B (each a meta tag of the other)
//
//	work  tags
//	1     Top
//	2     Mid
//	3     Leaf Syn
//	4     Other
//	5     Top Syn
//	6     Leaf, Other
//	7     Cyc B
func TestFindWorksTags(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	for id, names := range map[int64][]string{
		1: {"Fixture Top"}, 2: {"Fixture Mid"}, 3: {"Fixture Leaf Syn"}, 4: {"Fixture Other"},
		5: {"Fixture Top Syn"}, 6: {"Fixture Leaf", "Fixture Other"}, 7: {"Fixture Cyc B"},
	} {
		w := fixtureWork(id)
		w.Series, w.Tags = nil, nil
		for _, n := range names {
			w.Tags = append(w.Tags, model.Tag{Type: model.TagFreeform, Name: n})
		}
		w.Revised = fmt.Sprintf("2025-01-%02d", id) // newest first = highest id first
		_, err := s.UpsertWork(ctx, w, model.FromPage, 1)
		require.NoError(t, err)
	}
	tag := func(name string) int64 {
		t.Helper()
		r, err := s.TagByName(ctx, name)
		require.NoError(t, err)
		return r.ID
	}
	// In order: Cyc B's update creates Cyc A (no work uses it) before Cyc A's own update.
	for _, u := range []struct {
		name string
		u    TagInfoUpdate
	}{
		{"Fixture Leaf", TagInfoUpdate{MetaTags: []string{"Fixture Mid"}, Synonyms: []string{"Fixture Leaf Syn"}}},
		{"Fixture Mid", TagInfoUpdate{MetaTags: []string{"Fixture Top"}}},
		{"Fixture Top", TagInfoUpdate{Synonyms: []string{"Fixture Top Syn"}}},
		{"Fixture Cyc B", TagInfoUpdate{MetaTags: []string{"Fixture Cyc A"}}},
		{"Fixture Cyc A", TagInfoUpdate{MetaTags: []string{"Fixture Cyc B"}}},
	} {
		u.u.Type, u.u.Canonical = model.TagFreeform, true
		require.NoError(t, s.UpdateTagInfo(ctx, tag(u.name), u.u))
	}
	top, mid, leaf, leafSyn, other := tag("Fixture Top"), tag("Fixture Mid"), tag("Fixture Leaf"),
		tag("Fixture Leaf Syn"), tag("Fixture Other")
	for _, tc := range []struct {
		name          string
		tags, exclude []int64
		want          []int64
	}{
		{"meta tag: itself, subs (transitive) and synonyms of each", []int64{top}, nil, []int64{6, 5, 3, 2, 1}},
		{"mid tag", []int64{mid}, nil, []int64{6, 3, 2}},
		{"leaf tag and its synonym", []int64{leaf}, nil, []int64{6, 3}},
		{"synonym id: only works tagged with it", []int64{leafSyn}, nil, []int64{3}},
		{"every included tag must match", []int64{top, other}, nil, []int64{6}},
		{"cycle terminates", []int64{tag("Fixture Cyc A")}, nil, []int64{7}},
		{"unknown id", []int64{999_999}, nil, nil},
		{"exclude", nil, []int64{mid}, []int64{7, 5, 4, 1}},
		{"exclude any of", nil, []int64{leaf, other}, []int64{7, 5, 2, 1}},
		{"include and exclude", []int64{top}, []int64{other}, []int64{5, 3, 2, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := model.WorkFilter{Tags: tc.tags, ExcludeTags: tc.exclude, Limit: 10}
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
		"sort":            {Sort: "fixture_sort", Limit: 1},
		"direction":       {Direction: "up", Limit: 1},
	} {
		_, err := s.FindWorks(ctx, f)
		require.Error(t, err, name)
	}
	// Bad filters fail the count too.
	for name, f := range map[string]model.WorkFilter{
		"status":                 {Statuses: []model.WorkStatus{"gone"}, Limit: 1},
		"too many":               {Statuses: append(model.WorkStatuses(), model.StatusOK), Limit: 1},
		"too many tags":          {Tags: make([]int64, model.MaxWorkFilterTags+1), Limit: 1},
		"too many excluded tags": {ExcludeTags: make([]int64, model.MaxWorkFilterTags+1), Limit: 1},
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
		SingleChapter: true, Crossover: new(false)},
}

// TestWorksTagFilterPlans checks that expanding a tag filter uses the 0005 indexes (no automatic
// index built per query, no work_tags row reads). An included tag drives the page from its works
// and sorts them (the cost grows with the matches, not the offset; see BenchmarkWorksPage).
func TestWorksTagFilterPlans(t *testing.T) {
	s, _, _ := openTest(t)
	for _, f := range []model.WorkFilter{{Tags: []int64{1}}, {ExcludeTags: []int64{1, 2}}} {
		where, args, err := workWhere(f)
		require.NoError(t, err)
		order, err := workOrder(f)
		require.NoError(t, err)
		plan := explain(t, s, `SELECT id FROM works`+where+order+` LIMIT 1 OFFSET 1`, args...)
		for _, want := range []string{"COVERING INDEX work_tags_tag_work", "INDEX tag_meta_meta",
			"COVERING INDEX tags_merger"} {
			require.Contains(t, plan, want, plan)
		}
		require.NotContains(t, plan, "AUTOMATIC", plan)
	}
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
	// Tags (synthetic names): 1 popular (every 3rd work), 2 rare (every 1000th), 3 a meta tag
	// over subs 10..109 (sub k on works with i % 1000 = k, ~10% in all), 200..299 synonyms of
	// those subs, and 1000..1999 filler (four per work).
	_, err = s.meta.ExecContext(context.Background(), `
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 1999)
		INSERT INTO tags (id, name, type, canonical, merger_id, first_seen)
		SELECT i, 'Fixture Tag ' || i, 'Freeform', i NOT BETWEEN 200 AND 299,
			CASE WHEN i BETWEEN 200 AND 299 THEN i - 190 END, 1
		FROM n WHERE i <= 3 OR i BETWEEN 10 AND 109 OR i BETWEEN 200 AND 299 OR i >= 1000;
		INSERT INTO tag_meta (sub_id, meta_id) SELECT id, 3 FROM tags WHERE id BETWEEN 10 AND 109;
		INSERT INTO work_tags (work_id, tag_id, position) SELECT id, 1, 0 FROM works WHERE id % 3 = 0;
		INSERT INTO work_tags (work_id, tag_id, position) SELECT id, 2, 1 FROM works WHERE id % 1000 = 0;
		INSERT INTO work_tags (work_id, tag_id, position)
			SELECT id, id % 1000 + CASE WHEN id % 2 = 0 THEN 0 ELSE 190 END, 2 FROM works
			WHERE id % 1000 BETWEEN 10 AND 109;
		INSERT OR IGNORE INTO work_tags (work_id, tag_id, position)
			SELECT w.id, 1000 + (w.id * k.m) % 1000, 2 + k.p FROM works w,
			(SELECT 7 AS m, 1 AS p UNION ALL SELECT 13, 2 UNION ALL SELECT 101, 3 UNION ALL SELECT 997, 4) k;
	`)
	require.NoError(b, err)
	return s
}

var benchWorkFilters = map[string]model.WorkFilter{
	"none": {},
	"ok":   {Statuses: []model.WorkStatus{model.StatusOK}},
	"ok,complete,english": {Statuses: []model.WorkStatus{model.StatusOK}, Complete: new(true),
		Language: "English"},
	"single,fetched":    {SingleChapter: true, Fetched: new(true)},
	"words>=100k":       {Words: model.IntRange{Min: new(100_000)}},
	"revised 2020":      {Revised: model.DateRange{From: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2020, 12, 31, 0, 0, 0, 0, time.UTC)}},
	"tag popular":       {Tags: []int64{1}},
	"tag rare":          {Tags: []int64{2}},
	"tag meta":          {Tags: []int64{3}},
	"tags popular+meta": {Tags: []int64{1, 3}},
	"exclude popular":   {ExcludeTags: []int64{1}},
	"exclude meta+rare": {ExcludeTags: []int64{3, 2}},
	"popular,ok,-rare":  {Tags: []int64{1}, Statuses: []model.WorkStatus{model.StatusOK}, ExcludeTags: []int64{2}},
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
