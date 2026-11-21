package handlers

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/convert"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

// GetSeries is GET /api/v1/series/{id}: a stored series' page metadata, creators and how much
// of it the mirror holds, as AO3's series page.
func (a *Handlers) GetSeries(r *http.Request) (any, error) {
	id, err := runtime.OnlyPathID(r, dto.ParamID)
	if err != nil {
		return nil, err
	}
	v, err := a.ctl.GetSeries(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.SeriesView(v), nil
}

// ListSeriesWorks is GET /api/v1/series/{id}/works (page, per_page): one page of a stored
// series' works in part order, as AO3's series page lists them.
func (a *Handlers) ListSeriesWorks(r *http.Request) (any, error) {
	q, err := runtime.ParseParams(r, string(dto.ParamPage), string(dto.ParamPerPage))
	if err != nil {
		return nil, err
	}
	id := q.PathID(dto.ParamID)
	p := q.Page()
	if err := q.Err(); err != nil {
		return nil, err
	}
	items, total, err := a.ctl.ListSeriesWorks(r.Context(), id, int(p.Offset()), int(p.PerPage))
	if err != nil {
		return nil, err
	}
	return convert.WorkBlurbList(items, total, p.Page, p.PerPage), nil
}
