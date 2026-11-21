package model

import "time"

// WorkSort is a work listing's sort column, after AO3's work_search sort_column values.
type WorkSort string

const (
	WorkSortRevised   WorkSort = "revised"   // AO3 revised_at ("Date Updated"); the default
	WorkSortPublished WorkSort = "published" // AO3 created_at ("Date Posted")
	WorkSortWords     WorkSort = "words"     // AO3 word_count
	WorkSortHits      WorkSort = "hits"
	WorkSortKudos     WorkSort = "kudos" // AO3 kudos_count
	WorkSortComments  WorkSort = "comments"
	WorkSortBookmarks WorkSort = "bookmarks"
	WorkSortTitle     WorkSort = "title"   // AO3 title_to_sort_on
	WorkSortCreator   WorkSort = "creator" // AO3 authors_to_sort_on ("Creator")
	// WorkSortBestMatch ranks by how well a work matches WorkFilter.Query (AO3 _score, "Best
	// Match"), so it needs one. It is the default sort when there is a query.
	WorkSortBestMatch WorkSort = "best_match"
)

// WorkSorts lists every WorkSort that orders any listing, which is all but WorkSortBestMatch.
func WorkSorts() []WorkSort {
	return []WorkSort{WorkSortRevised, WorkSortPublished, WorkSortWords, WorkSortHits, WorkSortKudos,
		WorkSortComments, WorkSortBookmarks, WorkSortTitle, WorkSortCreator}
}

// DefaultDirection is the sort's direction when none is given, as on AO3: ascending for title
// and creator, descending for the rest.
func (s WorkSort) DefaultDirection() SortDirection {
	if s == WorkSortTitle || s == WorkSortCreator {
		return SortAsc
	}
	return SortDesc
}

// SortDirection orders a listing. The zero value is the sort column's default
// (WorkSort.DefaultDirection).
type SortDirection string

const (
	SortDefault SortDirection = ""
	SortAsc     SortDirection = "asc"
	SortDesc    SortDirection = "desc"
)

// IntRange bounds an integer column; a nil end is open. Both ends are inclusive.
type IntRange struct {
	Min, Max *int
}

// DateRange bounds a YYYY-MM-DD date column by calendar day (the time of day is ignored); a zero
// end is open. Both ends are inclusive.
type DateRange struct {
	From, To time.Time
}

// MaxWorkStatuses bounds WorkFilter.Statuses (one of each status).
var MaxWorkStatuses = len(WorkStatuses())

// MaxWorkFilterTags bounds WorkFilter.Tags and WorkFilter.ExcludeTags (each): the API's 20 per
// list plus the path tag of a tag's works route.
const MaxWorkFilterTags = 21

// WorkFilter selects and orders works, after AO3's work_search filters; zero fields match
// everything. Offset and Limit only apply to store.FindWorks (CountWorks ignores them).
//
// A tag id matches like AO3's filter_ids: works tagged with the tag, with a synonym of it
// (merger), or with any of its sub tags (transitively) or their synonyms. AO3 folds its per-type
// id lists (rating_ids, fandom_ids, …) into one filter_ids list, so the type grouping belongs to
// the API, not here.
type WorkFilter struct {
	// Query is search text (see searchquery.Terms) matched against titles, summaries, creators and
	// tags, like AO3's query field. It holds fanwork content: never log it.
	Query         string
	Tags          []int64      // every one must match (AO3 filter_ids; at most MaxWorkFilterTags)
	ExcludeTags   []int64      // none may match (AO3 excluded_tag_ids; at most MaxWorkFilterTags)
	CreatorID     int64        // 0 = any; else works with this creator (pseud) in the byline (AO3 pseud_ids)
	CollectionID  int64        // 0 = any; else works in this collection (AO3 collection_ids)
	Complete      *bool        // AO3 complete
	Crossover     *bool        // AO3 crossover: no fandom on the work is related to all the others
	SingleChapter bool         // AO3 single_chapter: one chapter expected
	Language      string       // exact match on the stored language
	Statuses      []WorkStatus // any of these (at most MaxWorkStatuses)
	Fetched       *bool        // true: full page fetched (chapters stored); false: listing blurbs only
	Words         IntRange
	Kudos         IntRange
	Hits          IntRange
	Comments      IntRange
	Bookmarks     IntRange
	Revised       DateRange // AO3 date_from / date_to
	Published     DateRange // excludes works with no known published date (blurb-only)
	Sort          WorkSort  // "" = WorkSortBestMatch with a Query, else WorkSortRevised
	Direction     SortDirection
	Offset        int // rows to skip (≥ 0)
	Limit         int // max rows (≥ 1)
}

// SortOrDefault is f's sort column: f.Sort, or the default when it is empty.
func (f WorkFilter) SortOrDefault() WorkSort {
	switch {
	case f.Sort != "":
		return f.Sort
	case f.Query != "":
		return WorkSortBestMatch
	default:
		return WorkSortRevised
	}
}
