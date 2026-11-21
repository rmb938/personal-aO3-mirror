package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// CreatorList maps one page of a creator listing.
func CreatorList(items []store.CreatorRow, total int, page, perPage int64) dto.List[dto.Creator] {
	return list(items, creatorListItem, total, page, perPage)
}

func creatorListItem(r store.CreatorRow) dto.Creator {
	return creator(r.CreatorRef, r.Works)
}
