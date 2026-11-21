package dto

import "time"

// TagType is an AO3 tag type (an enum).
type TagType string

// EnumValues lists every TagType.
func (TagType) EnumValues() []string {
	return []string{"Rating", "ArchiveWarning", "Category", "Fandom", "Relationship", "Character", "Freeform", "Media",
		"Unknown"}
}

// Tag is one stored tag with what its AO3 tag page shows (GET /api/v1/tags/{id} and
// /api/v1/tags/by-name/{name}). Every string but the enums and the date is fanwork content,
// hashed unless revealed.
type Tag struct {
	ID   int64   `json:"id"`
	Name string  `json:"name"`
	Type TagType `json:"type"`
	// Canonical is null until the tag page was read (a tag merged as a synonym is not
	// canonical).
	Canonical *bool `json:"canonical"`
	// Merger is the tag this one is a synonym of, or null.
	Merger *TagRef `json:"merger"`
	// The related tags are grouped by type in AO3's order, each group holding at most 100 tags
	// by name with the group's total. MetaTags and SubTags are direct only.
	Parents  []TagGroup `json:"parents"`
	Children []TagGroup `json:"children"`
	MetaTags []TagGroup `json:"meta_tags"`
	SubTags  []TagGroup `json:"sub_tags"`
	Synonyms []TagGroup `json:"synonyms"`
	// WorksCount is the stored works (any status) a filter on this tag matches: its synonyms
	// and sub tags count too.
	WorksCount int `json:"works_count"`
	// InfoFetchedAt is the last tag page fetch, also one that yielded nothing (a 404 or a
	// restricted page); omitted when never fetched.
	InfoFetchedAt time.Time `json:"info_fetched_at,omitzero"`
}

// TagRef is a reference to a stored tag.
type TagRef struct {
	ID   int64   `json:"id"`
	Name string  `json:"name"`
	Type TagType `json:"type"`
}

// TagGroup is a tag's related tags of one type: the first ones by name and how many there are.
type TagGroup struct {
	Type  TagType  `json:"type"`
	Total int      `json:"total"`
	Tags  []TagRef `json:"tags"`
}
