package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
)

// CreatorFilter maps a validated creator query body to the store filter (paging is the
// caller's).
func CreatorFilter(r request.CreatorQuery) model.CreatorFilter {
	return model.CreatorFilter{
		Name:      r.Name,
		Sort:      model.CreatorSort(r.Sort),
		Direction: model.SortDirection(r.Direction),
	}
}
