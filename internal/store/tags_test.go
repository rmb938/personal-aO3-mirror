package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

func TestTagInfo(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	_, err := s.UpsertWork(ctx, fixtureWork(1), model.FromBlurb, 1)
	require.NoError(t, err)
	w2 := fixtureWork(2)
	w2.Tags = []model.Tag{{Type: model.TagCharacter, Name: "Fixture Character"}}
	_, err = s.UpsertWork(ctx, w2, model.FromBlurb, 1)
	require.NoError(t, err)

	types := []string{"Character", "Fandom"}
	total, fetched, due, err := s.TagInfoProgress(ctx, types, clk.t.Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, []int{2, 0, 2}, []int{total, fetched, due}, "progress before (total, fetched, due)")

	need, err := s.TagsNeedingInfo(ctx, types, clk.t.Add(-time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, need, 2)
	require.Equal(t, "Fixture Character", need[0].Name, "character used by 2 works sorts first")
	char := need[0]
	require.NoError(t, s.UpdateTagInfo(ctx, char.ID, TagInfoUpdate{Type: model.TagCharacter, Canonical: true,
		Parents: []string{"Fixture Fandom"}, MetaTags: []string{"Fixture Meta"}, Synonyms: []string{"FC"}}))

	var canonical, parents, metas int
	var fc int64
	require.NoError(t, s.meta.QueryRow(`SELECT canonical FROM tags WHERE id = ?`, char.ID).Scan(&canonical))
	require.NoError(t, s.meta.QueryRow(`SELECT COUNT(*) FROM tag_parents tp JOIN tags p ON p.id = tp.parent_id
		WHERE tp.child_id = ? AND p.name = 'Fixture Fandom'`, char.ID).Scan(&parents))
	require.NoError(t, s.meta.QueryRow(`SELECT COUNT(*) FROM tag_meta WHERE sub_id = ?`, char.ID).Scan(&metas))
	require.NoError(t, s.meta.QueryRow(`SELECT merger_id FROM tags WHERE name = 'FC'`).Scan(&fc))
	require.Equal(t, 1, canonical)
	require.Equal(t, 1, parents)
	require.Equal(t, 1, metas)
	require.Equal(t, char.ID, fc, "synonym merged into the fetched tag")

	// The fetched tag no longer needs info, and its now-typed synonym/meta tags (used by no
	// mirrored work) must not get their own tag page fetches.
	need, err = s.TagsNeedingInfo(ctx, []string{"Character"}, clk.t.Add(-time.Hour), 10)
	require.NoError(t, err)
	require.Empty(t, need, "only tags used by mirrored works should need info")
	// Character fetched; the fandom tag (type Fandom, used by work 1) is still due. Synonym/meta
	// tags typed from the tag page are not counted (no mirrored work uses them).
	total, fetched, due, err = s.TagInfoProgress(ctx, types, clk.t.Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, []int{2, 1, 1}, []int{total, fetched, due}, "progress after (total, fetched, due)")

	// Synonym page: merger recorded, existing fandom type unchanged by Unknown.
	var fandomID int64
	require.NoError(t, s.meta.QueryRow(`SELECT id FROM tags WHERE name = 'Fixture Fandom'`).Scan(&fandomID))
	require.NoError(t, s.UpdateTagInfo(ctx, fandomID, TagInfoUpdate{Type: model.TagUnknown, MergerOf: "Canonical Fandom"}))
	var typ string
	require.NoError(t, s.meta.QueryRow(`SELECT type FROM tags WHERE id = ?`, fandomID).Scan(&typ))
	require.Equal(t, "Fandom", typ)

	// Same-type relations inherit the tag's type; parents (mixed types) stay Unknown unless known.
	got := map[string]string{}
	for _, n := range []string{"FC", "Fixture Meta", "Canonical Fandom"} {
		var ty string
		require.NoError(t, s.meta.QueryRow(`SELECT type FROM tags WHERE name = ?`, n).Scan(&ty))
		got[n] = ty
	}
	require.Equal(t, map[string]string{"FC": "Character", "Fixture Meta": "Character", "Canonical Fandom": "Fandom"}, got)
}

// A fandom's parents are media (AO3 gives a fandom no other parent type); other tags' parents
// stay Unknown until they appear on a work.
func TestTagInfoMediaParents(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	w := fixtureWork(1)
	w.Tags = []model.Tag{{Type: model.TagFandom, Name: "Fixture Fandom"}, {Type: model.TagFandom, Name: "Fixture Fandom 2"},
		{Type: model.TagCharacter, Name: "Fixture Character"}}
	_, err := s.UpsertWork(ctx, w, model.FromBlurb, 1)
	require.NoError(t, err)
	id := func(name string) int64 {
		var id int64
		require.NoError(t, s.meta.QueryRow(`SELECT id FROM tags WHERE name = ?`, name).Scan(&id))
		return id
	}
	typeOf := func(name string) string {
		var ty string
		require.NoError(t, s.meta.QueryRow(`SELECT type FROM tags WHERE name = ?`, name).Scan(&ty))
		return ty
	}

	require.NoError(t, s.UpdateTagInfo(ctx, id("Fixture Fandom"), TagInfoUpdate{Type: model.TagFandom, Canonical: true,
		Parents: []string{"Fixture Media"}}))
	require.Equal(t, "Media", typeOf("Fixture Media"))
	// The page's type is unknown, but the stored tag is a fandom.
	require.NoError(t, s.UpdateTagInfo(ctx, id("Fixture Fandom 2"), TagInfoUpdate{Type: model.TagUnknown,
		Parents: []string{"Fixture Media 2"}}))
	require.Equal(t, "Media", typeOf("Fixture Media 2"))
	require.NoError(t, s.UpdateTagInfo(ctx, id("Fixture Character"), TagInfoUpdate{Type: model.TagCharacter,
		Parents: []string{"Fixture Fandom", "Fixture Parent"}}))
	require.Equal(t, "Unknown", typeOf("Fixture Parent"), "a character's parents mix types")
	require.Equal(t, "Fandom", typeOf("Fixture Fandom"), "a known type is kept")
}
