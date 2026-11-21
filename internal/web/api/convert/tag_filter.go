package convert

import (
	"slices"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
)

// TagFilter maps a validated tag query body to the store filter (paging is the caller's).
func TagFilter(r request.TagQuery) model.TagFilter {
	var types []model.TagType
	for _, t := range r.Types {
		types = append(types, model.TagType(t))
	}
	return model.TagFilter{
		Name:      r.Name,
		Types:     types,
		Canonical: r.Canonical,
		ParentIDs: slices.Clone(r.ParentIDs),
		Sort:      model.TagSort(r.Sort),
		Direction: model.SortDirection(r.Direction),
	}
}
