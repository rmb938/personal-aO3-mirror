package handlers

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/convert"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

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
