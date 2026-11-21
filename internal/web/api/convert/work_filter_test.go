package convert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
)

func TestWorkFilter(t *testing.T) {
	assert.Equal(t, model.WorkFilter{}, WorkFilter(request.WorkQuery{}), "{} filters nothing")

	lo, hi, yes, no := 10, 20, true, false
	in := request.WorkQuery{
		TagIDs: []int64{1, 2}, ExcludeTagIDs: []int64{3}, Complete: &yes, Crossover: &no,
		SingleChapter: true, Language: "Fixture Language", Statuses: []dto.WorkStatus{"ok", "hidden"},
		Fetched: &no, Words: request.IntRange{Min: &lo, Max: &hi}, Kudos: request.IntRange{Min: &lo},
		Hits: request.IntRange{Max: &hi}, Comments: request.IntRange{Min: &hi}, Bookmarks: request.IntRange{Max: &lo},
		Revised:   request.DateRange{From: "2025-01-02", To: "2025-03-04"},
		Published: request.DateRange{To: "2024-12-31"},
		Sort:      "title", Direction: "desc",
	}
	got := WorkFilter(in)
	assert.Equal(t, model.WorkFilter{
		Tags: []int64{1, 2}, ExcludeTags: []int64{3}, Complete: &yes, Crossover: &no, SingleChapter: true,
		Language: "Fixture Language", Statuses: []model.WorkStatus{model.StatusOK, model.StatusHidden},
		Fetched: &no, Words: model.IntRange{Min: &lo, Max: &hi}, Kudos: model.IntRange{Min: &lo},
		Hits: model.IntRange{Max: &hi}, Comments: model.IntRange{Min: &hi}, Bookmarks: model.IntRange{Max: &lo},
		Revised: model.DateRange{From: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			To: time.Date(2025, 3, 4, 0, 0, 0, 0, time.UTC)},
		Published: model.DateRange{To: time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)},
		Sort:      model.WorkSortTitle, Direction: model.SortDesc,
	}, got)

	got.Tags[0] = 99
	assert.Equal(t, int64(1), in.TagIDs[0], "the filter owns its id lists")
}

// The store's tag cap is the request's plus the path tag of /tags/{id}/works, so a valid body
// never makes FindWorks fail (statuses are checked by TestEnumSetsMatchModel).
func TestWorkQueryBoundsMatchModel(t *testing.T) {
	assert.Equal(t, model.MaxWorkFilterTags, request.MaxWorkQueryTags+1)
}

func TestWorkFilterQuery(t *testing.T) {
	got := WorkFilter(request.WorkQuery{Q: `lorem -"ipsum dolor"`, Sort: "best_match"})
	assert.Equal(t, model.WorkFilter{Query: `lorem -"ipsum dolor"`, Sort: model.WorkSortBestMatch}, got)
}
