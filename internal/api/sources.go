package api

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/api/convert"
	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/api/request"
)

// listSources is GET /api/v1/sources (page, per_page).
func (a *API) listSources(r *http.Request) (any, error) {
	q, err := parseParams(r, string(dto.ParamPage), string(dto.ParamPerPage))
	if err != nil {
		return nil, err
	}
	p := q.page()
	if err := q.Err(); err != nil {
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
	q, err := parseParams(r)
	if err != nil {
		return nil, err
	}
	id := q.pathID(dto.ParamID)
	if err := q.Err(); err != nil {
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
	q, err := parseParams(r)
	if err != nil {
		return nil, err
	}
	id := q.pathID(dto.ParamJobID)
	if err := q.Err(); err != nil {
		return nil, err
	}
	o, err := a.ctl.SourceAddOutcome(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.SourceAdd(id, o), nil
}

// addSource is POST /api/v1/sources: it queues a source add for the daemon (202, Location of
// the add) or, for a single work, adds it at once (201, Location of the source). confirm is
// accepted only on the admin socket: a large source added publicly waits for `source confirm`.
func (a *API) addSource(admin bool) handlerFunc {
	return func(r *http.Request) (any, error) {
		q, err := parseParams(r)
		if err != nil {
			return nil, err
		}
		var req request.SourceAdd
		if err := decodeJSON(r, &req, &q.Validation); err != nil {
			return nil, err
		}
		if req.Confirm && !admin {
			q.Member(detailConfirmAdminOnly, "confirm")
		}
		if err := q.Err(); err != nil {
			return nil, err
		}
		added, err := a.ctl.QueueSourceAdd(r.Context(), req.URL, convert.AddSourceOptions(req))
		if err != nil {
			return nil, err
		}
		if added.Added != nil {
			return &Response{Status: http.StatusCreated, Location: pathFor(routeSource, added.Added.Source.ID),
				Body: convert.AddSourceResult(*added.Added)}, nil
		}
		return &Response{Status: http.StatusAccepted, Location: pathFor(routeSourceAdd, added.JobID),
			Body: convert.QueuedSourceAdd(added)}, nil
	}
}
