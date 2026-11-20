package request

import "github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"

// SourceRefresh is the body of POST /api/v1/sources/{id}/refresh (admin socket). `{}` is a
// plain refresh.
type SourceRefresh struct {
	// Full re-walks the whole listing now (a rescan) and clears a parse-failure block.
	Full bool `json:"full,omitzero"`
}

// Validate records nothing: every decodable body is valid.
func (*SourceRefresh) Validate(*problem.Validation) {}
