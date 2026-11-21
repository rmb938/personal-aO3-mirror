package request

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestTagQueryValid(t *testing.T) {
	maxParents := make([]int64, MaxTagQueryParents)
	for i := range maxParents {
		maxParents[i] = int64(i + 1)
	}
	allTypes := make([]dto.TagType, 0, len(dto.TagType("").EnumValues()))
	for _, v := range dto.TagType("").EnumValues() {
		allTypes = append(allTypes, dto.TagType(v))
	}
	for _, q := range []TagQuery{
		{}, // every tag
		{Name: "Fixture Tag", Types: allTypes, Canonical: new(true)},
		{Name: strings.Repeat("a ", MaxTagNameQueryWords), Sort: "uses", Direction: "asc"},
		{Name: strings.Repeat("é", MaxTagNameQueryRunes)},
		{ParentIDs: maxParents, Canonical: new(false), Sort: "name"},
		{ParentIDs: []int64{MaxID}},
	} {
		assert.Empty(t, errorsOf(t, &q), "%+v", q)
	}
}

func TestTagQueryInvalid(t *testing.T) {
	tooMany := make([]int64, MaxTagQueryParents+1)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	for name, tc := range map[string]struct {
		in   TagQuery
		want map[string]string
	}{
		"name blank":     {TagQuery{Name: "   "}, map[string]string{"/name": detailTagName.Message()}},
		"name control":   {TagQuery{Name: "a\tb"}, map[string]string{"/name": detailTagName.Message()}},
		"name long":      {TagQuery{Name: strings.Repeat("x", MaxTagNameQueryRunes+1)}, map[string]string{"/name": detailTagName.Message()}},
		"name too wordy": {TagQuery{Name: strings.Repeat("a ", MaxTagNameQueryWords+1)}, map[string]string{"/name": detailTagName.Message()}},
		"unknown type": {TagQuery{Types: []dto.TagType{"Fandom", "fandom"}},
			map[string]string{"/types/1": detailTagType.Message()}},
		"repeated type":   {TagQuery{Types: []dto.TagType{"Fandom", "Fandom"}}, map[string]string{"/types": detailTagTypes.Message()}},
		"too many parent": {TagQuery{ParentIDs: tooMany}, map[string]string{"/parent_ids": detailParents.Message()}},
		"repeated parent": {TagQuery{ParentIDs: []int64{3, 3}}, map[string]string{"/parent_ids": detailParents.Message()}},
		"parent out of range": {TagQuery{ParentIDs: []int64{0, 1, MaxID + 1}},
			map[string]string{"/parent_ids/0": detailTagID.Message(), "/parent_ids/2": detailTagID.Message()}},
		"sort and direction": {TagQuery{Sort: "created_at", Direction: "up"},
			map[string]string{"/sort": detailTagSort.Message(), "/direction": detailDir.Message()}},
	} {
		assert.Equal(t, tc.want, errorsOf(t, &tc.in), name)
	}
}

func TestTagQueryNeedsReveal(t *testing.T) {
	assert.False(t, (&TagQuery{Types: []dto.TagType{"Fandom"}, ParentIDs: []int64{1}, Sort: "uses"}).NeedsReveal(),
		"structured members only")
	assert.True(t, (&TagQuery{Name: "x"}).NeedsReveal(), "name is free text")
}
