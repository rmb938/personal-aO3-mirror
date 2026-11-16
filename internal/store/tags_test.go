package store

import (
	"context"
	"testing"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

func TestTagInfo(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	s.UpsertWork(ctx, fixtureWork(1), model.FromBlurb, 1)
	w2 := fixtureWork(2)
	w2.Tags = []model.Tag{{Type: model.TagCharacter, Name: "Fixture Character"}}
	s.UpsertWork(ctx, w2, model.FromBlurb, 1)

	need, err := s.TagsNeedingInfo(ctx, []string{"Character", "Fandom"}, clk.t.Add(-time.Hour), 10)
	if err != nil || len(need) != 2 || need[0].Name != "Fixture Character" {
		t.Fatalf("need: %+v %v", need, err) // character used by 2 works sorts first
	}
	char := need[0]
	if err := s.UpdateTagInfo(ctx, char.ID, TagInfoUpdate{Type: model.TagCharacter, Canonical: true,
		Parents: []string{"Fixture Fandom"}, MetaTags: []string{"Fixture Meta"}, Synonyms: []string{"FC"}}); err != nil {
		t.Fatal(err)
	}
	var canonical, parents, metas int
	var fc int64
	s.Meta.QueryRow(`SELECT canonical FROM tags WHERE id = ?`, char.ID).Scan(&canonical)
	s.Meta.QueryRow(`SELECT COUNT(*) FROM tag_parents tp JOIN tags p ON p.id = tp.parent_id WHERE tp.child_id = ? AND p.name = 'Fixture Fandom'`, char.ID).Scan(&parents)
	s.Meta.QueryRow(`SELECT COUNT(*) FROM tag_meta WHERE sub_id = ?`, char.ID).Scan(&metas)
	s.Meta.QueryRow(`SELECT merger_id FROM tags WHERE name = 'FC'`).Scan(&fc)
	if canonical != 1 || parents != 1 || metas != 1 || fc != char.ID {
		t.Fatalf("canonical=%d parents=%d metas=%d fc_merger=%d", canonical, parents, metas, fc)
	}
	// The fetched tag no longer needs info, and its now-typed synonym/meta tags (used by no
	// mirrored work) must not get their own tag page fetches.
	need, _ = s.TagsNeedingInfo(ctx, []string{"Character"}, clk.t.Add(-time.Hour), 10)
	if len(need) != 0 {
		t.Fatalf("only tags used by mirrored works should need info: %+v", need)
	}
	// Synonym page: merger recorded, existing fandom type unchanged by Unknown.
	fandom := TagRef{}
	s.Meta.QueryRow(`SELECT id FROM tags WHERE name = 'Fixture Fandom'`).Scan(&fandom.ID)
	s.UpdateTagInfo(ctx, fandom.ID, TagInfoUpdate{Type: model.TagUnknown, MergerOf: "Canonical Fandom"})
	var typ string
	s.Meta.QueryRow(`SELECT type FROM tags WHERE id = ?`, fandom.ID).Scan(&typ)
	if typ != "Fandom" {
		t.Fatalf("type changed to %s", typ)
	}
	// Same-type relations inherit the tag's type; parents (mixed types) stay Unknown unless known.
	types := map[string]string{}
	for _, n := range []string{"FC", "Fixture Meta", "Canonical Fandom"} {
		var ty string
		s.Meta.QueryRow(`SELECT type FROM tags WHERE name = ?`, n).Scan(&ty)
		types[n] = ty
	}
	if types["FC"] != "Character" || types["Fixture Meta"] != "Character" || types["Canonical Fandom"] != "Fandom" {
		t.Fatalf("same-type relations not typed: %v", types)
	}
}
