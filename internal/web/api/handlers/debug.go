package handlers

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/convert"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

func (a *Handlers) DebugJob(r *http.Request) (any, error) {
	id, err := runtime.OnlyPathID(r, dto.ParamID)
	if err != nil {
		return nil, err
	}
	d, err := a.ctl.DebugJob(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.JobDebug(d), nil
}

func (a *Handlers) DebugWork(r *http.Request) (any, error) {
	id, err := runtime.OnlyPathID(r, dto.ParamID)
	if err != nil {
		return nil, err
	}
	d, err := a.ctl.DebugWork(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.WorkDebug(d), nil
}
