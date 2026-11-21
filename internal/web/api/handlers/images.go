package handlers

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

// errImageNeedsReveal refuses an image in hashed mode, as the enforcer would replace it, but
// before the file is read.
var errImageNeedsReveal = &problem.Error{Type: dto.TypeRevealRequired, Status: http.StatusForbidden,
	Detail: runtime.DetailNotJSON}

// GetImage is GET /api/v1/images/{sha256}: a downloaded image's bytes, with the Content-Type
// its host served. An image is fanwork content and not JSON, so it is served only revealed.
func (a *Handlers) GetImage(r *http.Request) (any, error) {
	q, err := runtime.ParseParams(r)
	if err != nil {
		return nil, err
	}
	sha := q.PathSHA256(dto.ParamSHA256)
	if err := q.Err(); err != nil {
		return nil, err
	}
	if !runtime.Revealed(r) {
		return nil, errImageNeedsReveal
	}
	img, err := a.ctl.GetImage(r.Context(), sha)
	if err != nil {
		return nil, err
	}
	return &runtime.Blob{ContentType: img.ContentType, Body: img.Body}, nil
}
