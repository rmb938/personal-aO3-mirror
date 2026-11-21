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
