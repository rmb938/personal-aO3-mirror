package model

// CreatorSort is a creator listing's sort column.
type CreatorSort string

const (
	CreatorSortName  CreatorSort = "name"  // pseud, then login, ignoring ASCII case; the default
	CreatorSortWorks CreatorSort = "works" // stored works the creator is on
)

// CreatorSorts lists every CreatorSort.
func CreatorSorts() []CreatorSort { return []CreatorSort{CreatorSortName, CreatorSortWorks} }

// DefaultDirection is the sort's direction when none is given: ascending by name, descending
// by works.
func (s CreatorSort) DefaultDirection() SortDirection {
	if s == CreatorSortWorks {
		return SortDesc
	}
	return SortAsc
}

// MaxCreatorNameWords bounds the words in CreatorFilter.Name.
const MaxCreatorNameWords = 16

// CreatorFilter selects and orders stored creators (pseuds); zero fields match everything.
// Offset and Limit only apply to store.FindCreators (CountCreators ignores them).
type CreatorFilter struct {
	// Name is words (NameWords) that must each appear in the creator's pseud or login,
	// ignoring ASCII case. A word matches anywhere, not only a whole word. It holds fanwork
	// content: never log it.
	Name      string
	Sort      CreatorSort // "" = CreatorSortName
	Direction SortDirection
	Offset    int // rows to skip (≥ 0)
	Limit     int // max rows (≥ 1)
}

// SortOrDefault is f's sort column: f.Sort, or CreatorSortName when it is empty.
func (f CreatorFilter) SortOrDefault() CreatorSort {
	if f.Sort == "" {
		return CreatorSortName
	}
	return f.Sort
}
