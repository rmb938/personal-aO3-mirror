package handlers

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/convert"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

// listJobs is GET /api/v1/jobs (state, kind, source_id, page, per_page). Jobs come newest
// first, except a state=queued listing, which is in queue order (oldest first).
func (a *Handlers) ListJobs(r *http.Request) (any, error) {
	q, err := runtime.ParseParams(r, string(dto.ParamState), string(dto.ParamKind), string(dto.ParamSourceID),
		string(dto.ParamPage), string(dto.ParamPerPage))
	if err != nil {
		return nil, err
	}
	f := model.JobFilter{
		State:    q.Enum(string(dto.ParamState), dto.JobState(""), runtime.DetailJobState),
		Kind:     q.Enum(string(dto.ParamKind), dto.JobKind(""), runtime.DetailJobKind),
		SourceID: q.Int(string(dto.ParamSourceID), 0, runtime.MaxID, runtime.DetailID),
	}
	p := q.Page()
	if err := q.Err(); err != nil {
		return nil, err
	}
	f.Offset, f.Limit = int(p.Offset()), int(p.PerPage)
	if f.State == string(dto.JobQueued) {
		f.Order = model.JobsOldest
	}
	items, total, err := a.ctl.ListJobs(r.Context(), f)
	if err != nil {
		return nil, err
	}
	return convert.JobList(items, total, p.Page, p.PerPage), nil
}

// getJob is GET /api/v1/jobs/{id}.
func (a *Handlers) GetJob(r *http.Request) (any, error) {
	id, err := runtime.OnlyPathID(r, dto.ParamID)
	if err != nil {
		return nil, err
	}
	j, err := a.ctl.GetJob(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.Job(j), nil
}
