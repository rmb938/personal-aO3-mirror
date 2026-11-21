package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// seedTagList is seedTagGraph plus work 2, tagged Fixture Fandom and Fixture Character, so those
// two have 2 uses. The other tags on works have 1, and the tags known only from tag pages
// (Fixture Media Parent, Fixture Char Meta, Fixture Char Syn) have 0.
func seedTagList(t *testing.T) (*testStore, func(name string) int64) {
	t.Helper()
	s, tag := seedTagGraph(t)
	w := fixtureWork(2)
	w.Tags = []model.Tag{{Type: model.TagFandom, Name: "Fixture Fandom"}, {Type: model.TagCharacter, Name: "Fixture Character"}}
	_, err := s.UpsertWork(context.Background(), w, model.FromPage, 1)
	require.NoError(t, err)
	return s, tag
}

func tagRowNames(rows []TagRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

func TestFindTags(t *testing.T) {
	s, tag := seedTagList(t)
	ctx := context.Background()
	yes, no := true, false
	byName := []string{"fixture char a", "Fixture Char B", "Fixture Char Meta", "Fixture Char Syn", "Fixture Character",
		"Fixture Fandom", "fixture freeform", "Fixture Media Parent", "Fixture Rating", "Fixture Rel"}
	for _, tc := range []struct {
		name string
		f    model.TagFilter
		want []string
	}{
		{"by name ignoring case by default", model.TagFilter{}, byName},
		{"by name descending", model.TagFilter{Direction: model.SortDesc}, []string{"Fixture Rel", "Fixture Rating",
			"Fixture Media Parent", "fixture freeform", "Fixture Fandom", "Fixture Character", "Fixture Char Syn",
			"Fixture Char Meta", "Fixture Char B", "fixture char a"}},
		{"by uses, most first, ties by name", model.TagFilter{Sort: model.TagSortUses}, []string{
			"Fixture Character", "Fixture Fandom",
			"fixture char a", "Fixture Char B", "fixture freeform", "Fixture Rating", "Fixture Rel",
			"Fixture Char Meta", "Fixture Char Syn", "Fixture Media Parent"}},
		{"by uses ascending, ties still by name", model.TagFilter{Sort: model.TagSortUses, Direction: model.SortAsc}, []string{
			"Fixture Char Meta", "Fixture Char Syn", "Fixture Media Parent",
			"fixture char a", "Fixture Char B", "fixture freeform", "Fixture Rating", "Fixture Rel",
			"Fixture Character", "Fixture Fandom"}},
		{"name words all match, anywhere, ignoring case", model.TagFilter{Name: "CHAR  b"}, []string{"Fixture Char B"}},
		{"name word inside a word", model.TagFilter{Name: "ract"}, []string{"Fixture Character"}},
		{"LIKE wildcards match themselves", model.TagFilter{Name: "%"}, nil},
		{"LIKE single-char wildcard matches itself", model.TagFilter{Name: "Fixture_Rel"}, nil},
		{"types", model.TagFilter{Types: []model.TagType{model.TagFandom, model.TagRelation}},
			[]string{"Fixture Fandom", "Fixture Rel"}},
		{"canonical", model.TagFilter{Canonical: &yes},
			[]string{"fixture char a", "Fixture Char B", "Fixture Character", "Fixture Rel"}},
		{"not canonical: recorded only", model.TagFilter{Canonical: &no}, []string{"Fixture Char Syn"}},
		{"a parent", model.TagFilter{ParentIDs: []int64{tag("Fixture Fandom")}},
			[]string{"fixture char a", "Fixture Char B", "Fixture Character", "Fixture Rel"}},
		{"every parent", model.TagFilter{ParentIDs: []int64{tag("Fixture Fandom"), tag("Fixture Character")}},
			[]string{"Fixture Rel"}},
		{"filters combine", model.TagFilter{Name: "char", Types: []model.TagType{model.TagCharacter}, Canonical: &yes},
			[]string{"fixture char a", "Fixture Char B", "Fixture Character"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.f
			f.Limit = MaxTagPage
			got, err := s.FindTags(ctx, f)
			require.NoError(t, err)
			require.Equal(t, tc.want, tagRowNames(got))
			n, err := s.CountTags(ctx, f)
			require.NoError(t, err)
			require.Equal(t, len(tc.want), n, "count")
		})
	}

	t.Run("pages", func(t *testing.T) {
		for _, sort := range model.TagSorts() {
			var all []string
			for off := 0; off < len(byName); off += 3 {
				got, err := s.FindTags(ctx, model.TagFilter{Sort: sort, Offset: off, Limit: 3})
				require.NoError(t, err)
				all = append(all, tagRowNames(got)...)
			}
			require.ElementsMatch(t, byName, all, "every tag once, sort %s", sort)
		}
	})

	t.Run("row fields", func(t *testing.T) {
		got, err := s.FindTags(ctx, model.TagFilter{Name: "Fixture Char", Limit: MaxTagPage})
		require.NoError(t, err)
		rows := map[string]TagRow{}
		for _, r := range got {
			rows[r.Name] = r
		}
		char, syn := rows["Fixture Character"], rows["Fixture Char Syn"]
		require.Equal(t, 2, char.Uses)
		require.Equal(t, model.TagCharacter, char.Type)
		require.True(t, char.Canonical && char.CanonicalKnown)
		require.False(t, char.InfoFetchedAt.IsZero())
		require.Equal(t, char.ID, syn.MergerID)
		require.Zero(t, syn.Uses)
		require.False(t, rows["Fixture Char Meta"].CanonicalKnown)
	})
}

func TestFindTagsRejects(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	words := ""
	for range model.MaxTagNameWords + 1 {
		words += "w "
	}
	for name, f := range map[string]model.TagFilter{
		"negative offset":  {Offset: -1, Limit: 1},
		"zero limit":       {},
		"limit over page":  {Limit: MaxTagPage + 1},
		"too many words":   {Name: words, Limit: 1},
		"only spaces":      {Name: "  ", Limit: 1},
		"too many types":   {Types: make([]model.TagType, model.MaxTagFilterTypes+1), Limit: 1},
		"too many parents": {ParentIDs: make([]int64, model.MaxTagFilterParents+1), Limit: 1},
		"unknown sort":     {Sort: "created", Limit: 1},
		"unknown dir":      {Direction: "up", Limit: 1},
	} {
		_, err := s.FindTags(ctx, f)
		require.Error(t, err, name)
	}
	_, err := s.CountTags(ctx, model.TagFilter{Name: "  "})
	require.Error(t, err)
}

// BenchmarkTags times tag pages and counts over seedWorks' tags (~2k on ~1M taggings) plus
// 50k unused ones:
//
//	go test ./internal/store -run '^$' -bench 'Tags' -benchtime 20x
func BenchmarkTags(b *testing.B) {
	s := seedWorks(b)
	ctx := context.Background()
	_, err := s.meta.ExecContext(ctx, `
		WITH RECURSIVE n(i) AS (SELECT 10000 UNION ALL SELECT i + 1 FROM n WHERE i < 59999)
		INSERT INTO tags (id, name, type, first_seen) SELECT i, 'Fixture Unused ' || i, 'Character', 1 FROM n`)
	require.NoError(b, err)
	for name, f := range map[string]model.TagFilter{
		"none":      {},
		"name word": {Name: "tag 1"},
		"type":      {Types: []model.TagType{model.TagFreeform}},
	} {
		for _, sort := range model.TagSorts() {
			for _, offset := range []int{0, 1000} {
				f := f
				f.Sort, f.Offset, f.Limit = sort, offset, MaxTagPage
				b.Run(fmt.Sprintf("page/%s/%s/offset=%d", name, sort, offset), func(b *testing.B) {
					for b.Loop() {
						_, err := s.FindTags(ctx, f)
						require.NoError(b, err)
					}
				})
			}
		}
		b.Run("count/"+name, func(b *testing.B) {
			for b.Loop() {
				_, err := s.CountTags(ctx, f)
				require.NoError(b, err)
			}
		})
	}
}

func TestTagListPlans(t *testing.T) {
	s, _, _ := openTest(t)
	for name, tc := range map[string]struct {
		f    model.TagFilter
		args []any
		want []string
	}{
		"by name walks the name index": {model.TagFilter{}, nil,
			[]string{"SCAN t USING COVERING INDEX sqlite_autoindex_tags_1", "work_tags_tag_work (tag_id=?)"}},
		"by uses counts over the covering index": {model.TagFilter{Sort: model.TagSortUses}, nil,
			[]string{"USING COVERING INDEX work_tags_tag_work (tag_id=?)"}},
		"a parent reads its children by index": {model.TagFilter{ParentIDs: []int64{1}}, []any{int64(1)},
			[]string{"tag_parents_parent (parent_id=?)"}},
	} {
		q, args, err := tagPageQuery(tc.f)
		require.NoError(t, err)
		require.Equal(t, tc.args, args, name)
		plan := explain(t, s, q, append(args, 10, 0)...)
		for _, w := range tc.want {
			require.Contains(t, plan, w, "%s:\n%s", name, plan)
		}
		require.NotContains(t, plan, "SCAN wt", "%s:\n%s", name, plan)
		require.NotContains(t, plan, "SCAN r", "%s:\n%s", name, plan)
	}
}
