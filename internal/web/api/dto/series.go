package dto

import "time"

// Series is one stored series (GET /api/v1/series/{id}): what its AO3 series page shows, plus
// how much of it the mirror holds. Every string but the dates is fanwork content (or, for
// MirrorSourceURL, a URL), hashed unless revealed. HTML fields hold AO3's markup as stored;
// clients must sanitize it.
//
// A series named by a work page whose series page was never fetched (LastFetchedAt omitted)
// has only its id and title: no creators, description, dates or stats.
type Series struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	// Creators are by creator id (AO3's series byline order is not stored).
	Creators []WorkCreator `json:"creators"`
	// SummaryHTML is AO3's "Description"; it and NotesHTML are omitted when the series has none.
	SummaryHTML string `json:"summary_html,omitzero"`
	NotesHTML   string `json:"notes_html,omitzero"`
	// BegunOn and UpdatedOn are omitted until the series page was fetched.
	BegunOn   string `json:"begun_on,omitzero"`
	UpdatedOn string `json:"updated_on,omitzero"`
	// Words, Bookmarks and Complete are AO3's (zero until the series page was fetched).
	Words     int  `json:"words"`
	Bookmarks int  `json:"bookmarks"`
	Complete  bool `json:"complete"`
	// CompleteLocally, WorksTotal, WorksMirrored, MissingPositions and MissingWorkIDs are as in
	// WorkSeries.
	CompleteLocally  bool    `json:"complete_locally"`
	WorksTotal       *int    `json:"works_total"`
	WorksMirrored    int     `json:"works_mirrored"`
	MissingPositions []int   `json:"missing_positions"`
	MissingWorkIDs   []int64 `json:"missing_work_ids"`
	// WorksStored is the series' stored works, fetched or known from listing blurbs only: the
	// total of GET /api/v1/series/{id}/works. WorksMirrored counts only the fetched ones.
	WorksStored int `json:"works_stored"`
	// MirrorSourceURL is the AO3 series URL to pass to POST /api/v1/sources to mirror the whole
	// series. It is always set: adding a series that already is a source fails with
	// already-exists.
	MirrorSourceURL string    `json:"mirror_source_url"`
	LastFetchedAt   time.Time `json:"last_fetched_at,omitzero"`
}
