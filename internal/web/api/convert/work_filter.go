package convert

import (
	"slices"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
)

// WorkFilter maps a validated work query body to the store filter (paging is the caller's).
func WorkFilter(r request.WorkQuery) model.WorkFilter {
	return model.WorkFilter{
		Query:         r.Q,
		Tags:          slices.Clone(r.TagIDs),
		ExcludeTags:   slices.Clone(r.ExcludeTagIDs),
		Complete:      r.Complete,
		Crossover:     r.Crossover,
		SingleChapter: r.SingleChapter,
		Language:      r.Language,
		Statuses:      workStatuses(r.Statuses),
		Fetched:       r.Fetched,
		Words:         intRange(r.Words),
		Kudos:         intRange(r.Kudos),
		Hits:          intRange(r.Hits),
		Comments:      intRange(r.Comments),
		Bookmarks:     intRange(r.Bookmarks),
		Revised:       dateRange(r.Revised),
		Published:     dateRange(r.Published),
		Sort:          model.WorkSort(r.Sort),
		Direction:     model.SortDirection(r.Direction),
	}
}

func workStatuses(in []dto.WorkStatus) []model.WorkStatus {
	if len(in) == 0 {
		return nil
	}
	out := make([]model.WorkStatus, len(in))
	for i, s := range in {
		out[i] = model.WorkStatus(s)
	}
	return out
}

func intRange(r request.IntRange) model.IntRange {
	return model.IntRange{Min: r.Min, Max: r.Max}
}

// dateRange maps validated bounds; an absent end stays the zero time (open).
func dateRange(r request.DateRange) model.DateRange {
	from, _ := request.ParseDate(r.From)
	to, _ := request.ParseDate(r.To)
	return model.DateRange{From: from, To: to}
}
