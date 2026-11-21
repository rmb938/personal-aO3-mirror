package handlers

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/convert"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

// ListMedia is GET /api/v1/media (page, per_page): one page of the stored media tags by name,
// each with its most used fandoms, as AO3's /media page.
func (a *Handlers) ListMedia(r *http.Request) (any, error) {
	q, err := runtime.ParseParams(r, string(dto.ParamPage), string(dto.ParamPerPage))
	if err != nil {
		return nil, err
	}
	p := q.Page()
	if err := q.Err(); err != nil {
		return nil, err
	}
	items, total, err := a.ctl.ListMedia(r.Context(), int(p.Offset()), int(p.PerPage))
	if err != nil {
		return nil, err
	}
	return convert.MediumList(items, total, p.Page, p.PerPage), nil
}

// ListMediumFandoms is GET /api/v1/media/{id}/fandoms (sort, direction, page, per_page): one
// page of the stored fandoms of a media tag, as AO3's /media/{name}/fandoms.
func (a *Handlers) ListMediumFandoms(r *http.Request) (any, error) {
	q, err := runtime.ParseParams(r, string(dto.ParamSort), string(dto.ParamDirection),
		string(dto.ParamPage), string(dto.ParamPerPage))
	if err != nil {
		return nil, err
	}
	id := q.PathID(dto.ParamID)
	f := model.TagFilter{
		Sort:      model.TagSort(q.Enum(string(dto.ParamSort), dto.TagSort(""), runtime.DetailTagSort)),
		Direction: model.SortDirection(q.Enum(string(dto.ParamDirection), dto.SortDirection(""), runtime.DetailDirection)),
	}
	p := q.Page()
	if err := q.Err(); err != nil {
		return nil, err
	}
	f.Offset, f.Limit = int(p.Offset()), int(p.PerPage)
	items, total, err := a.ctl.ListMediumFandoms(r.Context(), id, f)
	if err != nil {
		return nil, err
	}
	return convert.TagList(items, total, p.Page, p.PerPage), nil
}
