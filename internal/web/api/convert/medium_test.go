package convert

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestMediumList(t *testing.T) {
	items := []controllers.Medium{
		{Tag: store.TagInfo{ID: 4, Name: "Fixture Medium", Type: model.TagMedia},
			Fandoms: []store.TagRow{{ID: 3, Name: "Fixture Fandom", Type: model.TagFandom, Canonical: true,
				CanonicalKnown: true, Uses: 2}},
			FandomsTotal: 6},
		{Tag: store.TagInfo{ID: 8, Name: "Fixture Empty", Type: model.TagMedia}},
	}
	got := MediumList(items, 9, 2, 2)
	assert.Equal(t, dto.List[dto.Medium]{Items: []dto.Medium{
		{ID: 4, Name: "Fixture Medium", Fandoms: []dto.TagListItem{
			{ID: 3, Name: "Fixture Fandom", Type: "Fandom", Canonical: new(true), Uses: 2}}, FandomsTotal: 6},
		{ID: 8, Name: "Fixture Empty"},
	}, Page: 2, PerPage: 2, Total: 9}, got)
}

func TestMedium(t *testing.T) {
	got := medium(controllers.Medium{Tag: store.TagInfo{ID: 4, Type: model.TagMedia, Canonical: true,
		CanonicalKnown: true}})
	assert.Equal(t, dto.Medium{ID: 4, Canonical: new(true)}, got, "a recorded canonical flag is kept")
}
