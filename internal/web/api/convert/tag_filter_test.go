package convert

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
)

func TestTagFilter(t *testing.T) {
	assert.Equal(t, model.TagFilter{}, TagFilter(request.TagQuery{}), "{} filters nothing")

	no := false
	in := request.TagQuery{
		Name: "Fixture Tag", Types: []dto.TagType{"Fandom", "Media"}, Canonical: &no,
		ParentIDs: []int64{4, 5}, Sort: "uses", Direction: "asc",
	}
	got := TagFilter(in)
	assert.Equal(t, model.TagFilter{
		Name: "Fixture Tag", Types: []model.TagType{model.TagFandom, model.TagMedia}, Canonical: &no,
		ParentIDs: []int64{4, 5}, Sort: model.TagSortUses, Direction: model.SortAsc,
	}, got)

	got.ParentIDs[0] = 99
	assert.Equal(t, int64(4), in.ParentIDs[0], "the filter owns its id list")
}

// A valid body never makes FindTags fail: the request's caps are the store's (tag types are
// checked by TestEnumSetsMatchModel, and a list of distinct ones is never over the store's cap).
func TestTagQueryBoundsMatchModel(t *testing.T) {
	assert.Equal(t, model.MaxTagNameWords, request.MaxTagNameQueryWords)
	assert.Equal(t, model.MaxTagFilterParents, request.MaxTagQueryParents)
	assert.LessOrEqual(t, len(dto.TagType("").EnumValues()), model.MaxTagFilterTypes)
}
