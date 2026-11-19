// Package model holds the domain types shared by the parsers and the store.
// Values of these types contain fanwork content: never log their text fields.
package model

// TagType mirrors AO3 tag classes (app/models/tag.rb subclasses).
type TagType string

const (
	TagRating    TagType = "Rating"
	TagWarning   TagType = "ArchiveWarning"
	TagCategory  TagType = "Category"
	TagFandom    TagType = "Fandom"
	TagRelation  TagType = "Relationship"
	TagCharacter TagType = "Character"
	TagFreeform  TagType = "Freeform"
	TagMedia     TagType = "Media"
	TagUnknown   TagType = "Unknown"
)

// TagTypes lists every TagType.
func TagTypes() []TagType {
	return []TagType{TagRating, TagWarning, TagCategory, TagFandom, TagRelation, TagCharacter, TagFreeform, TagMedia,
		TagUnknown}
}

// WorkStatus is the local availability state of a work.
type WorkStatus string

const (
	StatusOK         WorkStatus = "ok"
	StatusDeleted    WorkStatus = "deleted"    // 404 on AO3; local copy kept
	StatusHidden     WorkStatus = "hidden"     // hidden by admin / access denied
	StatusRestricted WorkStatus = "restricted" // archive-locked; needs login
	StatusUnrevealed WorkStatus = "unrevealed" // in an unrevealed collection
)

// WorkStatuses lists every WorkStatus (the works.status CHECK).
func WorkStatuses() []WorkStatus {
	return []WorkStatus{StatusOK, StatusDeleted, StatusHidden, StatusRestricted, StatusUnrevealed}
}

// Origin says where a Work value was parsed from; it controls which fields are authoritative.
type Origin int

const (
	FromBlurb Origin = iota // listing/series/bookmark blurb: metadata + stats, no chapters/published date
	FromPage                // full work page: everything
)

type Tag struct {
	Type TagType
	Name string
}

type Creator struct {
	User  string // AO3 login, e.g. from /users/<login>/pseuds/<pseud>
	Pseud string
}

type SeriesPart struct {
	ID       int64
	Title    string
	Position int
}

type Collection struct {
	Name  string // URL name, /collections/<name>
	Title string
}

type Stats struct {
	Kudos     int
	Hits      int
	Comments  int
	Bookmarks int
}

type Chapter struct {
	ID           int64 // AO3 chapter id; 0 if unknown (single-chapter works)
	Position     int   // 1-based
	Title        string
	Published    string // YYYY-MM-DD, if known
	SummaryHTML  string
	BodyHTML     string
	NotesHTML    string
	EndNotesHTML string
}

// Work is everything known about a work from one parse.
type Work struct {
	ID                 int64
	Title              string
	Creators           []Creator
	Anonymous          bool
	Restricted         bool
	SummaryHTML        string
	NotesHTML          string
	EndNotesHTML       string
	Language           string
	Tags               []Tag
	Words              int
	ChaptersPosted     int
	ChaptersExpected   int // 0 = unknown ("?")
	Complete           bool
	Published          string // YYYY-MM-DD (page only)
	Revised            string // YYYY-MM-DD: "Updated"/"Completed" on page, datetime on blurb
	RevisedApprox      bool   // Revised came from a relative blurb date ("3 days"); not exact
	RevisedIsPublished bool   // page had no Updated/Completed date (single chapter): Revised = Published
	Stats              Stats
	Series             []SeriesPart
	Collections        []Collection
	Chapters           []Chapter // page only
	SkinCSS            string    // page only
}

// Series is a series page's metadata (series/show.html.erb). "Description" on AO3 is SummaryHTML.
type Series struct {
	ID          int64
	Title       string
	Creators    []Creator
	Begun       string // YYYY-MM-DD
	Updated     string // YYYY-MM-DD
	SummaryHTML string // AO3 "Description"
	NotesHTML   string
	Words       int
	WorksCount  int
	Complete    bool
	Bookmarks   int
}
