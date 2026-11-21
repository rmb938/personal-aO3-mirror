package store

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// seedTagGraph stores one work and tag info linking its tags:
//
//	Fixture Character: parents Fixture Fandom, Fixture Media Parent (Unknown); meta Fixture Char
//	  Meta; synonym Fixture Char Syn
//	fixture char a, Fixture Char B: parent Fixture Fandom
//	Fixture Rel: parents Fixture Fandom, Fixture Character
func seedTagGraph(t *testing.T) (*testStore, func(name string) int64) {
	t.Helper()
	s, _, _ := openTest(t)
	ctx := context.Background()
	w := fixtureWork(1)
	w.Tags = append(w.Tags, model.Tag{Type: model.TagCharacter, Name: "Fixture Char B"},
		model.Tag{Type: model.TagCharacter, Name: "fixture char a"}, model.Tag{Type: model.TagRelation, Name: "Fixture Rel"})
	_, err := s.UpsertWork(ctx, w, model.FromPage, 1)
	require.NoError(t, err)
	tag := func(name string) int64 {
		t.Helper()
		r, err := s.TagByName(ctx, name)
		require.NoError(t, err)
		return r.ID
	}
	for name, u := range map[string]TagInfoUpdate{
		"Fixture Character": {Type: model.TagCharacter, Parents: []string{"Fixture Fandom", "Fixture Media Parent"},
			MetaTags: []string{"Fixture Char Meta"}, Synonyms: []string{"Fixture Char Syn"}},
		"Fixture Char B": {Type: model.TagCharacter, Parents: []string{"Fixture Fandom"}},
		"fixture char a": {Type: model.TagCharacter, Parents: []string{"Fixture Fandom"}},
		"Fixture Rel":    {Type: model.TagRelation, Parents: []string{"Fixture Fandom", "Fixture Character"}},
	} {
		u.Canonical = true
		require.NoError(t, s.UpdateTagInfo(ctx, tag(name), u))
	}
	return s, tag
}

func TestTagInfoByID(t *testing.T) {
	s, tag := seedTagGraph(t)
	ctx := context.Background()
	char := tag("Fixture Character")

	got, err := s.TagInfoByID(ctx, char)
	require.NoError(t, err)
	require.Equal(t, "Fixture Character", got.Name)
	require.Equal(t, model.TagCharacter, got.Type)
	require.True(t, got.Canonical)
	require.True(t, got.CanonicalKnown)
	require.Zero(t, got.MergerID)
	require.False(t, got.InfoFetchedAt.IsZero())

	syn, err := s.TagInfoByID(ctx, tag("Fixture Char Syn"))
	require.NoError(t, err)
	require.Equal(t, char, syn.MergerID)
	require.False(t, syn.Canonical)
	require.True(t, syn.CanonicalKnown, "merged as a synonym: known not canonical")
	require.True(t, syn.InfoFetchedAt.IsZero(), "its own page was never read")

	rating, err := s.TagInfoByID(ctx, tag("Fixture Rating"))
	require.NoError(t, err)
	require.False(t, rating.CanonicalKnown, "no tag info yet")

	_, err = s.TagInfoByID(ctx, 999_999)
	require.ErrorIs(t, err, ErrNoTag)
}

// groupNames is groups as "Type:total=name,name" strings.
func groupNames(groups []TagGroup) []string {
	var out []string
	for _, g := range groups {
		var names []string
		for _, r := range g.Tags {
			names = append(names, r.Name)
		}
		out = append(out, string(g.Type)+":"+strconv.Itoa(g.Total)+"="+strings.Join(names, ","))
	}
	return out
}

func TestRelatedTags(t *testing.T) {
	s, tag := seedTagGraph(t)
	ctx := context.Background()
	fandom, char, meta := tag("Fixture Fandom"), tag("Fixture Character"), tag("Fixture Char Meta")
	for _, tc := range []struct {
		name  string
		id    int64
		rel   TagRelation
		limit int
		want  []string
	}{
		{"children grouped in tag type order, by name ignoring case", fandom, TagChildren, MaxTagGroup, []string{
			"Relationship:1=Fixture Rel", "Character:3=fixture char a,Fixture Char B,Fixture Character",
		}},
		{"limit cuts each group, total counts all", fandom, TagChildren, 2, []string{
			"Relationship:1=Fixture Rel", "Character:3=fixture char a,Fixture Char B",
		}},
		{"parents of mixed types", char, TagParents, MaxTagGroup, []string{
			"Fandom:1=Fixture Fandom", "Unknown:1=Fixture Media Parent",
		}},
		{"children of a character", char, TagChildren, MaxTagGroup, []string{"Relationship:1=Fixture Rel"}},
		{"meta tags", char, TagMetaTags, MaxTagGroup, []string{"Character:1=Fixture Char Meta"}},
		{"sub tags", meta, TagSubTags, MaxTagGroup, []string{"Character:1=Fixture Character"}},
		{"synonyms", char, TagSynonyms, MaxTagGroup, []string{"Character:1=Fixture Char Syn"}},
		{"none", fandom, TagParents, MaxTagGroup, nil},
		{"unknown tag", 999_999, TagChildren, MaxTagGroup, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.RelatedTags(ctx, tc.id, tc.rel, tc.limit)
			require.NoError(t, err)
			require.Equal(t, tc.want, groupNames(got))
		})
	}
}

func TestRelatedTagsRejects(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	for _, limit := range []int{0, MaxTagGroup + 1} {
		_, err := s.RelatedTags(ctx, 1, TagChildren, limit)
		require.Error(t, err, "limit %d", limit)
	}
	_, err := s.RelatedTags(ctx, 1, TagRelation(99), 1)
	require.Error(t, err)
}

func TestRelatedTagsPlans(t *testing.T) {
	s, _, _ := openTest(t)
	for rel, want := range map[TagRelation]string{
		TagParents:  "SEARCH r USING COVERING INDEX sqlite_autoindex_tag_parents_1 (child_id=?)",
		TagChildren: "SEARCH r USING INDEX tag_parents_parent (parent_id=?)",
		TagMetaTags: "SEARCH r USING COVERING INDEX sqlite_autoindex_tag_meta_1 (sub_id=?)",
		TagSubTags:  "SEARCH r USING INDEX tag_meta_meta (meta_id=?)",
		TagSynonyms: "SEARCH t USING INDEX tags_merger (merger_id=?)",
	} {
		q, err := relatedTagsQuery(rel)
		require.NoError(t, err)
		plan := explain(t, s, q, 1, 1)
		require.Contains(t, plan, want, plan)
		require.NotContains(t, plan, "SCAN t", plan)
		require.NotContains(t, plan, "SCAN r", plan)
	}
}
