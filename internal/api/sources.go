package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/rmb938/personal-aO3-mirror/internal/api/convert"
	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
)

// listSources is GET /api/v1/sources (page, per_page).
func (a *API) listSources(r *http.Request) (any, error) {
	q, err := parseQuery(r, string(dto.ParamPage), string(dto.ParamPerPage))
	if err != nil {
		return nil, err
	}
	p, err := q.page()
	if err != nil {
		return nil, err
	}
	items, total, err := a.ctl.ListSources(r.Context(), int(p.Offset()), int(p.PerPage))
	if err != nil {
		return nil, err
	}
	return convert.SourceList(items, total, p.Page, p.PerPage), nil
}

// getSource is GET /api/v1/sources/{id}.
func (a *API) getSource(r *http.Request) (any, error) {
	if _, err := parseQuery(r); err != nil {
		return nil, err
	}
	id, err := pathID(r, dto.ParamID)
	if err != nil {
		return nil, err
	}
	s, err := a.ctl.GetSource(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.Source(s), nil
}

// getSourceAdd is GET /api/v1/source-adds/{job_id}: where a queued source add stands.
func (a *API) getSourceAdd(r *http.Request) (any, error) {
	if _, err := parseQuery(r); err != nil {
		return nil, err
	}
	id, err := pathID(r, dto.ParamJobID)
	if err != nil {
		return nil, err
	}
	o, err := a.ctl.SourceAddOutcome(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.SourceAdd(id, o), nil
}

// pathID parses the route's ID path parameter name.
func pathID(r *http.Request, name dto.ParamName) (int64, error) {
	return parseID(string(name), chi.URLParam(r, string(name)))
}
