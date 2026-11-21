package handlers

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/convert"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

// GetTag is GET /api/v1/tags/{id}: a stored tag with its merger, related tags and work count.
func (a *Handlers) GetTag(r *http.Request) (any, error) {
	id, err := runtime.OnlyPathID(r, dto.ParamID)
	if err != nil {
		return nil, err
	}
	v, err := a.ctl.GetTag(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.TagView(v), nil
}

// GetTagByName is GET /api/v1/tags/by-name/{name}: GetTag for the tag AO3's /tags/{name} page
// shows. The name is free text, so it needs the reveal parameter.
func (a *Handlers) GetTagByName(r *http.Request) (any, error) {
	q, err := runtime.ParseParams(r)
	if err != nil {
		return nil, err
	}
	name := q.PathTagName(dto.ParamTagName)
	if err := q.Err(); err != nil {
		return nil, err
	}
	if !runtime.Revealed(r) {
		return nil, problem.WithDetail(dto.TypeRevealRequired, runtime.DetailFreeTextPath)
	}
	v, err := a.ctl.GetTagByName(r.Context(), name)
	if err != nil {
		return nil, err
	}
	return convert.TagView(v), nil
}
