package dto

// Creator is one stored creator: an AO3 pseud, as the work view's creators items, with how many
// stored works it is on. It is GET /api/v1/creators/{id} and an item of QUERY /api/v1/creators.
// User and Pseud are fanwork content and MirrorSourceURL holds them, so all three are hashed
// unless revealed.
type Creator struct {
	ID int64 `json:"id"`
	// User is the AO3 login, Pseud the name shown in bylines.
	User  string `json:"user"`
	Pseud string `json:"pseud"`
	// Works is the stored works (any status) with the creator in the byline: the total of
	// GET /api/v1/creators/{id}/works. A creator named only by a series page has 0.
	Works int `json:"works"`
	// MirrorSourceURL is the AO3 pseud works URL to pass to POST /api/v1/sources to mirror the
	// creator's works. It is always set: adding a pseud that already is a source fails with
	// already-exists.
	MirrorSourceURL string `json:"mirror_source_url"`
}

// CreatorSort is a creator listing's sort column (an enum): name (pseud, then login, ignoring
// case; the default, ascending by default) or works (descending by default).
type CreatorSort string

// EnumValues lists every CreatorSort.
func (CreatorSort) EnumValues() []string { return []string{"name", "works"} }
