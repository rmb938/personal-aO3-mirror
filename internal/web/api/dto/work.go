package dto

import "time"

// Work is one stored work: its metadata, relations and chapter list, without chapter bodies
// (GET /api/v1/works/{id}). Every string but the enums and dates is fanwork content, hashed
// unless revealed. HTML fields hold AO3's markup as stored; clients must sanitize it.
type Work struct {
	ID     int64      `json:"id"`
	Status WorkStatus `json:"status"`
	Title  string     `json:"title"`
	// Creators are in byline order ([] for an anonymous work or an orphaned one).
	Creators   []WorkCreator `json:"creators"`
	Anonymous  bool          `json:"anonymous"`
	Restricted bool          `json:"restricted"`
	// SummaryHTML, NotesHTML and EndNotesHTML are omitted when the work has none.
	SummaryHTML  string `json:"summary_html,omitzero"`
	NotesHTML    string `json:"notes_html,omitzero"`
	EndNotesHTML string `json:"end_notes_html,omitzero"`
	Language     string `json:"language,omitzero"`
	Words        int    `json:"words"`
	// ChaptersPosted is AO3's count; ChaptersExpected is omitted when unknown ("?").
	ChaptersPosted   int  `json:"chapters_posted"`
	ChaptersExpected int  `json:"chapters_expected,omitzero"`
	Complete         bool `json:"complete"`
	// PublishedOn is known only from the work page; RevisedOn is the last update.
	PublishedOn string    `json:"published_on,omitzero"`
	RevisedOn   string    `json:"revised_on,omitzero"`
	Stats       WorkStats `json:"stats"`
	// Tags are in page order (AO3 groups them by type).
	Tags        []WorkTag        `json:"tags"`
	Series      []WorkSeries     `json:"series"`
	Collections []WorkCollection `json:"collections"`
	// Chapters is the chapter list, in order. It is [] for a work known only from listing
	// blurbs (LastFetchedAt omitted): such a work has no chapters, notes or published date.
	Chapters      []ChapterRef `json:"chapters"`
	LastFetchedAt time.Time    `json:"last_fetched_at,omitzero"`
}

// WorkStats are a work's AO3 counters.
type WorkStats struct {
	Kudos     int `json:"kudos"`
	Hits      int `json:"hits"`
	Comments  int `json:"comments"`
	Bookmarks int `json:"bookmarks"`
}

// WorkCreator is one creator in a work's byline.
type WorkCreator struct {
	ID int64 `json:"id"`
	// User is the AO3 login, Pseud the name shown in the byline.
	User  string `json:"user"`
	Pseud string `json:"pseud"`
}

// WorkTag is one of a work's tags.
type WorkTag struct {
	ID   int64   `json:"id"`
	Name string  `json:"name"`
	Type TagType `json:"type"`
}

// WorkCollection is a collection a work is in.
type WorkCollection struct {
	ID int64 `json:"id"`
	// Name is the collection's URL name, Title its display title.
	Name  string `json:"name"`
	Title string `json:"title"`
}

// WorkSeries is a series a work belongs to, with how much of the series is mirrored
// (docs/plan.md, "flag partial series").
type WorkSeries struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	// Position is the work's part number in the series (omitted when unknown).
	Position int `json:"position,omitzero"`
	// CompleteLocally is false for a partial series: not a source, with fewer mirrored works
	// than AO3 lists. A series whose page was never fetched has no known total
	// (WorksTotal null), so it is not partial.
	CompleteLocally bool `json:"complete_locally"`
	WorksTotal      *int `json:"works_total"`
	WorksMirrored   int  `json:"works_mirrored"`
	// MissingPositions are the part numbers none of our works has; MissingWorkIDs are known
	// member works we have not fetched.
	MissingPositions []int   `json:"missing_positions"`
	MissingWorkIDs   []int64 `json:"missing_work_ids"`
}

// ChapterRef is one entry of a work's chapter list.
type ChapterRef struct {
	// ID is AO3's chapter id (omitted when unknown, e.g. a single-chapter work).
	ID       int64  `json:"id,omitzero"`
	Position int    `json:"position"`
	Title    string `json:"title,omitzero"`
	// PublishedOn is omitted when unknown.
	PublishedOn string `json:"published_on,omitzero"`
}

// WorkSkin is a work's skin (GET /api/v1/works/{id}/skin): untrusted CSS, to be sanitized
// and scoped by the client.
type WorkSkin struct {
	// CSS is omitted when the work has no skin.
	CSS string `json:"css,omitzero"`
}
