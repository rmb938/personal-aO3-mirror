package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSortOrDefault(t *testing.T) {
	assert.Equal(t, WorkSortRevised, WorkFilter{}.SortOrDefault())
	assert.Equal(t, WorkSortBestMatch, WorkFilter{Query: "lorem"}.SortOrDefault())
	assert.Equal(t, WorkSortKudos, WorkFilter{Query: "lorem", Sort: WorkSortKudos}.SortOrDefault())
	assert.NotContains(t, WorkSorts(), WorkSortBestMatch, "best_match needs a query")
}
