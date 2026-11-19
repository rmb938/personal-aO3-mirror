package api

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/api/convert"
	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// listJobs is GET /api/v1/jobs (state, kind, source_id, page, per_page). Jobs come newest
// first, except a state=queued listing, which is in queue order (oldest first).
func (a *API) listJobs(r *http.Request) (any, error) {
	q, err := parseParams(r, string(dto.ParamState), string(dto.ParamKind), string(dto.ParamSourceID),
		string(dto.ParamPage), string(dto.ParamPerPage))
	if err != nil {
		return nil, err
	}
	f := model.JobFilter{
		State:    q.enum(string(dto.ParamState), dto.JobState(""), detailJobState),
		Kind:     q.enum(string(dto.ParamKind), dto.JobKind(""), detailJobKind),
		SourceID: q.int(string(dto.ParamSourceID), 0, MaxID, detailID),
	}
	p := q.page()
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
func (a *API) getJob(r *http.Request) (any, error) {
	q, err := parseParams(r)
	if err != nil {
		return nil, err
	}
	id := q.pathID(dto.ParamID)
	if err := q.Err(); err != nil {
		return nil, err
	}
	j, err := a.ctl.GetJob(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.Job(j), nil
}
