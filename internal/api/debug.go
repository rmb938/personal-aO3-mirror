package api

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/api/convert"
	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
)

// debugJob is GET /api/v1/debug/jobs/{id} (admin socket): the job with its events and requests.
func (a *API) debugJob(r *http.Request) (any, error) {
	id, err := onlyPathID(r, dto.ParamID)
	if err != nil {
		return nil, err
	}
	d, err := a.ctl.DebugJob(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.JobDebug(d), nil
}

// debugWork is GET /api/v1/debug/works/{id} (admin socket): the work's diagnostic view with
// its fetch jobs and requests.
func (a *API) debugWork(r *http.Request) (any, error) {
	id, err := onlyPathID(r, dto.ParamID)
	if err != nil {
		return nil, err
	}
	d, err := a.ctl.DebugWork(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.WorkDebug(d), nil
}
