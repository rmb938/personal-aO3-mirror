package handlers

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/convert"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

// QueryCreators is QUERY /api/v1/creators (page, per_page; body request.CreatorQuery): one page
// of the stored creators (pseuds) matching the body's filters. The name filter is free text, so
// it needs the reveal parameter.
func (a *Handlers) QueryCreators(r *http.Request) (any, error) {
	q, err := runtime.ParseParams(r, string(dto.ParamPage), string(dto.ParamPerPage))
	if err != nil {
		return nil, err
	}
	p := q.Page()
	var req request.CreatorQuery
	if err := runtime.DecodeJSON(r, &req, &q.Validation); err != nil {
		return nil, err
	}
	if err := q.Err(); err != nil {
		return nil, err
	}
	if req.NeedsReveal() && !runtime.Revealed(r) {
		return nil, problem.WithDetail(dto.TypeRevealRequired, runtime.DetailFreeText)
	}
	f := convert.CreatorFilter(req)
	f.Offset, f.Limit = p.Offset(), p.PerPage
	items, total, err := a.ctl.ListCreators(r.Context(), f)
	if err != nil {
		return nil, err
	}
	return convert.CreatorList(items, total, p.Page, p.PerPage), nil
}

// GetCreator is GET /api/v1/creators/{id}: a stored creator (pseud) and its stored works count.
func (a *Handlers) GetCreator(r *http.Request) (any, error) {
	id, err := runtime.OnlyPathID(r, dto.ParamID)
	if err != nil {
		return nil, err
	}
	v, err := a.ctl.GetCreator(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.CreatorView(v), nil
}

// ListCreatorWorks is GET /api/v1/creators/{id}/works (sort, direction, page, per_page):
// ListWorks for the works with the creator in the byline, as AO3's
// /users/{login}/pseuds/{pseud}/works.
func (a *Handlers) ListCreatorWorks(r *http.Request) (any, error) {
	return a.listWorks(r, a.ctl.ListCreatorWorks)
}

// QueryCreatorWorks is QUERY /api/v1/creators/{id}/works: QueryWorks within the creator's works.
func (a *Handlers) QueryCreatorWorks(r *http.Request) (any, error) {
	return a.queryWorks(r, a.ctl.ListCreatorWorks)
}
