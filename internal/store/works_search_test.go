package store

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/searchquery"
)

// seedSearchWorks stores synthetic works with "lorem" in a different field each, plus one
// without it:
//
//	id  lorem in  other text              complete kudos
//	1   title     -                       yes      10
//	2   summary   "ipsum dolor"           no       40
//	3   creator   -                       no       30
//	4   tag       "dolor ipsum"           yes      20
//	5   -         "ipsum", "lorem" absent no       50
func seedSearchWorks(t *testing.T) *testStore {
	t.Helper()
	s, _, _ := openTest(t)
	ctx := context.Background()
	for id, f := range map[int64]func(*model.Work){
		1: func(w *model.Work) { w.Title, w.Complete = "Fixture Lorem", true },
		2: func(w *model.Work) { w.SummaryHTML = "<p>Fixture lorem <b>ipsum dolor</b> sit</p>" },
		3: func(w *model.Work) { w.Creators = []model.Creator{{User: "lorem_author", Pseud: "fixture_pseud"}} },
		4: func(w *model.Work) {
			w.Tags = []model.Tag{{Type: model.TagFreeform, Name: "Fixture Lorem Tag"}}
			w.SummaryHTML, w.Complete = "<p>Fixture dolor ipsum sit</p>", true
		},
		5: func(w *model.Work) { w.SummaryHTML = "<p>Fixture ipsum</p>" },
	} {
		w := fixtureWork(id)
		w.Title, w.SummaryHTML, w.Series = "Fixture Title", "<p>Fixture summary</p>", nil
		w.Creators = []model.Creator{{User: "fixture_author", Pseud: "fixture_author"}}
		w.Tags = []model.Tag{{Type: model.TagFreeform, Name: "Fixture Tag"}}
		w.Stats.Kudos = int(id%5) * 10
		if id == 5 {
			w.Stats.Kudos = 50
		}
		f(&w)
		_, err := s.UpsertWork(ctx, w, model.FromPage, 1)
		require.NoError(t, err)
	}
	return s
}

func TestFindWorksSearch(t *testing.T) {
	s := seedSearchWorks(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		f    model.WorkFilter
		want []int64
	}{
		// AO3's boosts: title^7, creators^5, then summary and tags (equal weight; the shorter
		// column wins under bm25, so the tag before the summary).
		{"best match by default", model.WorkFilter{Query: "lorem"}, []int64{1, 3, 4, 2}},
		{"best match worst first", model.WorkFilter{Query: "lorem", Direction: model.SortAsc}, []int64{2, 4, 3, 1}},
		{"case and diacritics folded", model.WorkFilter{Query: "LÖRÉM", Sort: model.WorkSortKudos}, []int64{4, 3, 2, 1}},
		{"other sort", model.WorkFilter{Query: "lorem", Sort: model.WorkSortKudos}, []int64{4, 3, 2, 1}},
		{"every term must match", model.WorkFilter{Query: "lorem ipsum", Sort: model.WorkSortKudos}, []int64{4, 2}},
		{"terms across fields", model.WorkFilter{Query: "lorem_author fixture", Sort: model.WorkSortKudos}, []int64{3}},
		{"phrase", model.WorkFilter{Query: `"ipsum dolor"`}, []int64{2}},
		{"reversed phrase", model.WorkFilter{Query: `"dolor ipsum"`}, []int64{4}},
		{"exclude word", model.WorkFilter{Query: "ipsum -lorem"}, []int64{5}},
		{"exclude phrase", model.WorkFilter{Query: `ipsum -"ipsum dolor"`, Sort: model.WorkSortKudos}, []int64{5, 4}},
		{"exclude two", model.WorkFilter{Query: "fixture -lorem -ipsum"}, nil},
		{"with filters", model.WorkFilter{Query: "lorem", Complete: new(true)}, []int64{1, 4}},
		{"with filters, other sort", model.WorkFilter{Query: "lorem", Complete: new(false), Sort: model.WorkSortKudos},
			[]int64{3, 2}},
		{"no match", model.WorkFilter{Query: "amet"}, nil},
		// No FTS5 syntax gets through: each is a quoted string of plain words.
		{"no column filter", model.WorkFilter{Query: "title:lorem"}, nil},
		{"no prefix", model.WorkFilter{Query: "lore*"}, nil},
		{"no OR", model.WorkFilter{Query: "lorem OR amet"}, nil},
		{"no NEAR", model.WorkFilter{Query: "NEAR(lorem ipsum)"}, nil},
		{"punctuation splits words", model.WorkFilter{Query: "lorem_author*"}, []int64{3}},
		{"quotes in a word", model.WorkFilter{Query: `lo"rem`}, nil},
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

	// Pages of a ranked search keep the rank order.
	var paged []int64
	for off := range 4 {
		page, err := s.FindWorks(ctx, model.WorkFilter{Query: "lorem", Offset: off, Limit: 1})
		require.NoError(t, err)
		paged = append(paged, workIDs(page)...)
	}
	require.Equal(t, []int64{1, 3, 4, 2}, paged)
}

func TestFindWorksSearchRejects(t *testing.T) {
	s := seedSearchWorks(t)
	ctx := context.Background()
	for name, f := range map[string]model.WorkFilter{
		"best match, no query": {Sort: model.WorkSortBestMatch, Limit: 1},
		"no words":             {Query: `!!! "" -`, Limit: 1},
		"only excluded":        {Query: "-lorem", Limit: 1},
		"too many terms":       {Query: strings.Repeat("lorem ", searchquery.MaxTerms+1), Limit: 1},
	} {
		_, err := s.FindWorks(ctx, f)
		require.Error(t, err, name)
		if f.Query != "" {
			_, err = s.CountWorks(ctx, f)
			require.Error(t, err, name)
		}
	}
	_, err := s.FindWorks(ctx, model.WorkFilter{Query: strings.Repeat("lorem ", searchquery.MaxTerms), Limit: 1})
	require.NoError(t, err, "at the cap")
}

// seedWorksFTS indexes seedWorks's works with synthetic text: "fixture" in every work, "alpha"
// in every 3rd summary, "beta" in every 10th title and "rare" in every 1000th summary.
func seedWorksFTS(b *testing.B, s *testStore) {
	b.Helper()
	_, err := s.meta.ExecContext(context.Background(), `
		INSERT INTO works_fts (rowid, title, summary, creators, tags)
		SELECT id, title || CASE WHEN id % 10 = 0 THEN ' beta' ELSE '' END,
			'Fixture summary lorem ipsum' || CASE WHEN id % 3 = 0 THEN ' alpha' ELSE '' END
				|| CASE WHEN id % 1000 = 0 THEN ' rare' ELSE '' END,
			'fixture_author fixture_pseud', 'Fixture Tag ' || (id % 1000) || ' • Fixture Rating'
		FROM works`)
	require.NoError(b, err)
}

var benchSearches = map[string]model.WorkFilter{
	"every work":     {Query: "fixture"},
	"third":          {Query: "alpha"},
	"tenth":          {Query: "beta"},
	"rare":           {Query: "rare"},
	"third-rare":     {Query: "alpha -rare"},
	"phrase+third":   {Query: `"lorem ipsum" alpha`},
	"third,ok,tag":   {Query: "alpha", Statuses: []model.WorkStatus{model.StatusOK}, Tags: []int64{1}},
	"tenth,complete": {Query: "beta", Complete: new(true)},
}

// BenchmarkWorksSearch pages searches over benchWorks works.
//
//	go test ./internal/store -run '^$' -bench 'WorksSearch' -benchtime 20x
func BenchmarkWorksSearch(b *testing.B) {
	s := seedWorks(b)
	seedWorksFTS(b, s)
	ctx := context.Background()
	for name, f := range benchSearches {
		for _, sort := range []model.WorkSort{model.WorkSortBestMatch, model.WorkSortRevised} {
			for _, offset := range []int{0, 10_000} {
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
		b.Run(name+"/count", func(b *testing.B) {
			for b.Loop() {
				_, err := s.CountWorks(ctx, f)
				require.NoError(b, err)
			}
		})
	}
}

// TestWorksSearchPlans checks that a search page is driven by the FTS match: works are read by
// rowid (never scanned) and the matches sorted, so the cost grows with the matches, not the
// table or the offset (see BenchmarkWorksSearch).
func TestWorksSearchPlans(t *testing.T) {
	s, _, _ := openTest(t)
	for _, sort := range []model.WorkSort{model.WorkSortBestMatch, model.WorkSortRevised} {
		for i, f := range listPlanFilters {
			f.Query, f.Sort = "lorem", sort
			q, args, err := workPageQuery(f)
			require.NoError(t, err)
			plan := explain(t, s, q, append(args, 1, 1)...)
			for _, want := range []string{"SCAN works_fts VIRTUAL TABLE INDEX", "SEARCH works USING INTEGER PRIMARY KEY"} {
				require.Contains(t, plan, want, "%s filter %d: %s", sort, i, plan)
			}
			for _, bad := range []string{"AUTOMATIC", "SCAN works;", "SCAN works USING"} {
				require.NotContains(t, plan+";", bad, "%s filter %d: %s", sort, i, plan)
			}
		}
	}
}
