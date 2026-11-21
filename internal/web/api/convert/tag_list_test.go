package convert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestTagList(t *testing.T) {
	fetched := time.Date(2026, 11, 1, 2, 3, 4, 0, time.FixedZone("x", 3600))
	rows := []store.TagRow{
		{ID: 3, Name: "Fixture Fandom", Type: model.TagFandom, Canonical: true, CanonicalKnown: true,
			InfoFetchedAt: fetched, Uses: 7},
		{ID: 4, Name: "Fixture Synonym", Type: model.TagFandom, CanonicalKnown: true, MergerID: 3},
	}
	got := TagList(rows, 12, 2, 2)
	assert.Equal(t, dto.List[dto.TagListItem]{Items: []dto.TagListItem{
		{ID: 3, Name: "Fixture Fandom", Type: "Fandom", Canonical: new(true), Uses: 7, InfoFetchedAt: fetched.UTC()},
		{ID: 4, Name: "Fixture Synonym", Type: "Fandom", Canonical: new(false), MergerID: new(int64(3))},
	}, Page: 2, PerPage: 2, Total: 12}, got)
	assert.Equal(t, time.UTC, got.Items[0].InfoFetchedAt.Location())
}

func TestTagListItem(t *testing.T) {
	bare := tagListItem(store.TagRow{ID: 5, Type: model.TagUnknown})
	assert.Equal(t, dto.TagListItem{ID: 5, Type: "Unknown"}, bare, "canonical unknown (null), no merger (null), never fetched")
}
