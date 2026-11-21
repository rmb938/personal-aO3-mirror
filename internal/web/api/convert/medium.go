package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// MediumList maps one page of the media listing.
func MediumList(items []controllers.Medium, total int, page, perPage int64) dto.List[dto.Medium] {
	return list(items, medium, total, page, perPage)
}

func medium(m controllers.Medium) dto.Medium {
	return dto.Medium{
		ID:           m.Tag.ID,
		Name:         m.Tag.Name,
		Canonical:    canonical(m.Tag),
		Fandoms:      convSlice(m.Fandoms, tagListItem),
		FandomsTotal: m.FandomsTotal,
	}
}
