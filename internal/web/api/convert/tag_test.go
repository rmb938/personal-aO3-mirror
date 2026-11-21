package convert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestTagView(t *testing.T) {
	fetched := time.Date(2026, 11, 1, 2, 3, 4, 0, time.FixedZone("x", 3600))
	group := func(typ model.TagType, total int, ids ...int64) store.TagGroup {
		g := store.TagGroup{Type: typ, Total: total}
		for _, id := range ids {
			g.Tags = append(g.Tags, store.TagRef{ID: id, Name: "Fixture Tag", Type: typ})
		}
		return g
	}
	v := controllers.TagView{
		Tag: store.TagInfo{
			TagRef:    store.TagRef{ID: 3, Name: "Fixture Character", Type: model.TagCharacter},
			Canonical: true, CanonicalKnown: true, InfoFetchedAt: fetched,
		},
		Merger:   &store.TagRef{ID: 4, Name: "Fixture Merger", Type: model.TagCharacter},
		Parents:  []store.TagGroup{group(model.TagFandom, 2, 10, 11)},
		Children: []store.TagGroup{group(model.TagRelation, 1, 12), group(model.TagFreeform, 150, 13)},
		MetaTags: []store.TagGroup{group(model.TagCharacter, 1, 14)},
		SubTags:  []store.TagGroup{group(model.TagCharacter, 1, 15)},
		Synonyms: []store.TagGroup{group(model.TagCharacter, 1, 16)},
		Works:    42,
	}
	ref := func(id int64, typ dto.TagType) dto.TagRef { return dto.TagRef{ID: id, Name: "Fixture Tag", Type: typ} }
	got := TagView(v)
	assert.Equal(t, dto.Tag{
		ID: 3, Name: "Fixture Character", Type: "Character", Canonical: new(true),
		Merger:     &dto.TagRef{ID: 4, Name: "Fixture Merger", Type: "Character"},
		Parents:    []dto.TagGroup{{Type: "Fandom", Total: 2, Tags: []dto.TagRef{ref(10, "Fandom"), ref(11, "Fandom")}}},
		Children:   []dto.TagGroup{{Type: "Relationship", Total: 1, Tags: []dto.TagRef{ref(12, "Relationship")}}, {Type: "Freeform", Total: 150, Tags: []dto.TagRef{ref(13, "Freeform")}}},
		MetaTags:   []dto.TagGroup{{Type: "Character", Total: 1, Tags: []dto.TagRef{ref(14, "Character")}}},
		SubTags:    []dto.TagGroup{{Type: "Character", Total: 1, Tags: []dto.TagRef{ref(15, "Character")}}},
		Synonyms:   []dto.TagGroup{{Type: "Character", Total: 1, Tags: []dto.TagRef{ref(16, "Character")}}},
		WorksCount: 42, InfoFetchedAt: fetched.UTC(),
	}, got)
	assert.Equal(t, time.UTC, got.InfoFetchedAt.Location())

	bare := TagView(controllers.TagView{Tag: store.TagInfo{TagRef: store.TagRef{ID: 5, Type: model.TagUnknown}}})
	assert.Equal(t, dto.Tag{ID: 5, Type: "Unknown"}, bare, "canonical unknown (null), no merger, no relations, never fetched")
}

func TestCanonical(t *testing.T) {
	assert.Nil(t, canonical(store.TagInfo{Canonical: true}), "not recorded: unknown")
	assert.Equal(t, new(false), canonical(store.TagInfo{CanonicalKnown: true}))
	assert.Equal(t, new(true), canonical(store.TagInfo{Canonical: true, CanonicalKnown: true}))
}

func TestTagGroup(t *testing.T) {
	assert.Equal(t, dto.TagGroup{Type: "Fandom", Total: 3},
		tagGroup(store.TagGroup{Type: model.TagFandom, Total: 3}), "no tags: nil (written as [])")
}
