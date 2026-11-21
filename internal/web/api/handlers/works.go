package handlers

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/convert"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

// ListWorks is GET /api/v1/works (sort, direction, page, per_page): one page of every stored
// work, by default most recently revised first.
func (a *Handlers) ListWorks(r *http.Request) (any, error) {
	q, err := runtime.ParseParams(r, string(dto.ParamSort), string(dto.ParamDirection),
		string(dto.ParamPage), string(dto.ParamPerPage))
	if err != nil {
		return nil, err
	}
	f := model.WorkFilter{
		Sort:      model.WorkSort(q.Enum(string(dto.ParamSort), dto.WorkSort(""), runtime.DetailWorkSort)),
		Direction: model.SortDirection(q.Enum(string(dto.ParamDirection), dto.SortDirection(""), runtime.DetailDirection)),
	}
	if f.Sort == model.WorkSort(dto.WorkSortBestMatch) {
		q.Param(string(dto.ParamSort), request.DetailBestMatch) // GET has no q; QUERY does
	}
	p := q.Page()
	if err := q.Err(); err != nil {
		return nil, err
	}
	f.Offset, f.Limit = int(p.Offset()), int(p.PerPage)
	items, total, err := a.ctl.ListWorks(r.Context(), f)
	if err != nil {
		return nil, err
	}
	return convert.WorkBlurbList(items, total, p.Page, p.PerPage), nil
}

// QueryWorks is QUERY /api/v1/works (page, per_page; body request.WorkQuery): one page of the
// works matching the body's filters. A free-text member needs the reveal parameter.
func (a *Handlers) QueryWorks(r *http.Request) (any, error) {
	q, err := runtime.ParseParams(r, string(dto.ParamPage), string(dto.ParamPerPage))
	if err != nil {
		return nil, err
	}
	p := q.Page()
	var req request.WorkQuery
	if err := runtime.DecodeJSON(r, &req, &q.Validation); err != nil {
		return nil, err
	}
	if err := q.Err(); err != nil {
		return nil, err
	}
	if req.NeedsReveal() && !runtime.Revealed(r) {
		return nil, problem.WithDetail(dto.TypeRevealRequired, runtime.DetailFreeText)
	}
	f := convert.WorkFilter(req)
	f.Offset, f.Limit = int(p.Offset()), int(p.PerPage)
	items, total, err := a.ctl.ListWorks(r.Context(), f)
	if err != nil {
		return nil, err
	}
	return convert.WorkBlurbList(items, total, p.Page, p.PerPage), nil
}

// GetWork is GET /api/v1/works/{id}: a stored work's metadata, relations and chapter list.
func (a *Handlers) GetWork(r *http.Request) (any, error) {
	id, err := runtime.OnlyPathID(r, dto.ParamID)
	if err != nil {
		return nil, err
	}
	v, err := a.ctl.GetWork(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.WorkView(v), nil
}

// GetChapter is GET /api/v1/works/{id}/chapters/{position}: one chapter with its content.
func (a *Handlers) GetChapter(r *http.Request) (any, error) {
	q, err := runtime.ParseParams(r)
	if err != nil {
		return nil, err
	}
	id := q.PathID(dto.ParamID)
	pos := q.PathInt(dto.ParamPosition, runtime.MaxPosition, runtime.DetailPosition)
	if err := q.Err(); err != nil {
		return nil, err
	}
	c, err := a.ctl.GetChapter(r.Context(), id, int(pos))
	if err != nil {
		return nil, err
	}
	return convert.Chapter(c), nil
}

// GetWorkChapters is GET /api/v1/works/{id}/chapters: every chapter with its content, refused
// with work-too-large over controllers.MaxWorkChaptersBytes.
func (a *Handlers) GetWorkChapters(r *http.Request) (any, error) {
	id, err := runtime.OnlyPathID(r, dto.ParamID)
	if err != nil {
		return nil, err
	}
	cs, err := a.ctl.GetWorkChapters(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.WorkChapters(cs), nil
}

// GetWorkSkin is GET /api/v1/works/{id}/skin: a stored work's skin CSS (omitted for none).
func (a *Handlers) GetWorkSkin(r *http.Request) (any, error) {
	id, err := runtime.OnlyPathID(r, dto.ParamID)
	if err != nil {
		return nil, err
	}
	css, err := a.ctl.GetWorkSkin(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.WorkSkin(css), nil
}
