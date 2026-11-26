package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// TagList maps one page of a tag listing.
func TagList(items []store.TagRow, total, page, perPage int) dto.List[dto.TagListItem] {
	return list(items, tagListItem, total, page, perPage)
}

func tagListItem(r store.TagRow) dto.TagListItem {
	var merger *int64
	if r.MergerID != 0 {
		merger = new(r.MergerID)
	}
	return dto.TagListItem{
		ID:            r.ID,
		Name:          r.Name,
		Type:          dto.TagType(r.Type),
		Canonical:     canonical(r.TagInfo),
		MergerID:      merger,
		Uses:          r.Uses,
		InfoFetchedAt: utc(r.InfoFetchedAt),
	}
}
