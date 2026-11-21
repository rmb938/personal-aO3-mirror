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
)

// WorkSorts lists every WorkSort.
func WorkSorts() []WorkSort {
	return []WorkSort{WorkSortRevised, WorkSortPublished, WorkSortWords, WorkSortHits, WorkSortKudos,
		WorkSortComments, WorkSortBookmarks}
}

// SortDirection orders a listing. The zero value is the sort column's default (descending for
// every column so far, as on AO3).
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

// MaxWorkFilterTags bounds WorkFilter.Tags and WorkFilter.ExcludeTags (each).
const MaxWorkFilterTags = 20

// WorkFilter selects and orders works, after AO3's work_search filters; zero fields match
// everything. Offset and Limit only apply to store.FindWorks (CountWorks ignores them).
//
// A tag id matches like AO3's filter_ids: works tagged with the tag, with a synonym of it
// (merger), or with any of its sub tags (transitively) or their synonyms. AO3 folds its per-type
// id lists (rating_ids, fandom_ids, …) into one filter_ids list, so the type grouping belongs to
// the API, not here.
type WorkFilter struct {
	Tags          []int64      // every one must match (AO3 filter_ids; at most MaxWorkFilterTags)
	ExcludeTags   []int64      // none may match (AO3 excluded_tag_ids; at most MaxWorkFilterTags)
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
	Sort          WorkSort  // "" = WorkSortRevised
	Direction     SortDirection
	Offset        int // rows to skip (≥ 0)
	Limit         int // max rows (≥ 1)
}
