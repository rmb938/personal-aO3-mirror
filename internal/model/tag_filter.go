package model

import "strings"

// TagSort is a tag listing's sort column, after AO3's tag_search sort_column values. AO3's
// created_at sort has no stored counterpart (the mirror knows when it first saw a tag, not when
// AO3 created it), so it is left out.
type TagSort string

const (
	TagSortName TagSort = "name" // the default
	TagSortUses TagSort = "uses" // stored works tagged with the tag itself (AO3 uses)
)

// TagSorts lists every TagSort.
func TagSorts() []TagSort { return []TagSort{TagSortName, TagSortUses} }

// DefaultDirection is the sort's direction when none is given, as on AO3: ascending by name,
// descending by uses.
func (s TagSort) DefaultDirection() SortDirection {
	if s == TagSortUses {
		return SortDesc
	}
	return SortAsc
}

const (
	// MaxTagFilterParents bounds TagFilter.ParentIDs.
	MaxTagFilterParents = 20
	// MaxTagNameWords bounds the words in TagFilter.Name.
	MaxTagNameWords = 16
)

// MaxTagFilterTypes bounds TagFilter.Types (one of each type).
var MaxTagFilterTypes = len(TagTypes())

// TagFilter selects and orders stored tags, after AO3's tag search (tag_search); zero fields
// match everything. Offset and Limit only apply to store.FindTags (CountTags ignores them).
type TagFilter struct {
	// Name is words (NameWords) that must each appear in the tag's name, ignoring ASCII
	// case, like AO3's name field with its words ANDed. A word matches anywhere in the name,
	// not only a whole word. It holds fanwork content: never log it.
	Name string
	// Types: any of these (AO3 type). At most MaxTagFilterTypes.
	Types []TagType
	// Canonical: true keeps canonical tags, false tags recorded as not canonical (synonyms
	// among them). A tag whose page was never read has no recorded state and matches neither.
	Canonical *bool
	// ParentIDs must each be a direct parent of the tag (AO3 fandoms, by id here; also a media
	// tag's fandoms). At most MaxTagFilterParents.
	ParentIDs []int64
	Sort      TagSort // "" = TagSortName
	Direction SortDirection
	Offset    int // rows to skip (≥ 0)
	Limit     int // max rows (≥ 1)
}

// SortOrDefault is f's sort column: f.Sort, or TagSortName when it is empty.
func (f TagFilter) SortOrDefault() TagSort {
	if f.Sort == "" {
		return TagSortName
	}
	return f.Sort
}

// NameWords splits a tag or creator name filter into the words that must each match: runs of
// non-space characters.
func NameWords(name string) []string { return strings.Fields(name) }
