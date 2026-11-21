package dto

// Medium is one stored media tag on the media page (GET /api/v1/media), as AO3's /media: the
// medium and its most used fandoms. Names are fanwork content, hashed unless revealed.
type Medium struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Canonical is null until the tag page was read, as in Tag (the mirror does not read media
	// tag pages, so it is usually null).
	Canonical *bool `json:"canonical"`
	// Fandoms is at most 5 of the medium's fandoms, most used first (a tie by name), as
	// GET /api/v1/media/{id}/fandoms?sort=uses lists them.
	Fandoms []TagListItem `json:"fandoms"`
	// FandomsTotal is every stored fandom listing the medium as a parent.
	FandomsTotal int `json:"fandoms_total"`
}
