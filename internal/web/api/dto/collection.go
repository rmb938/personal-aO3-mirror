package dto

// Collection is one stored collection, as the work view's collections items, with how many
// stored works are in it. It is GET /api/v1/collections/{id}. Name and Title are fanwork content
// and MirrorSourceURL holds the name, so all three are hashed unless revealed.
type Collection struct {
	ID int64 `json:"id"`
	// Name is the collection's URL name, Title its display title.
	Name  string `json:"name"`
	Title string `json:"title"`
	// Works is the stored works (any status) in the collection: the total of
	// GET /api/v1/collections/{id}/works. A collection whose works all left it has 0.
	Works int `json:"works"`
	// MirrorSourceURL is the AO3 collection works URL to pass to POST /api/v1/sources to mirror
	// the collection. It is always set: adding a collection that already is a source fails with
	// already-exists.
	MirrorSourceURL string `json:"mirror_source_url"`
}
