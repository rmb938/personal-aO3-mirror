package handlers

import (
	"context"
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/convert"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

// listSources is GET /api/v1/sources (page, per_page).
func (a *Handlers) ListSources(r *http.Request) (any, error) {
	q, err := runtime.ParseParams(r, string(dto.ParamPage), string(dto.ParamPerPage))
	if err != nil {
		return nil, err
	}
	p := q.Page()
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
func (a *Handlers) GetSource(r *http.Request) (any, error) {
	id, err := runtime.OnlyPathID(r, dto.ParamID)
	if err != nil {
		return nil, err
	}
	s, err := a.ctl.GetSource(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.Source(s), nil
}

// getSourceAdd is GET /api/v1/source-adds/{job_id}: where a queued source add stands.
func (a *Handlers) GetSourceAdd(r *http.Request) (any, error) {
	id, err := runtime.OnlyPathID(r, dto.ParamJobID)
	if err != nil {
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
func (a *Handlers) AddSource(admin bool) runtime.HandlerFunc {
	return func(r *http.Request) (any, error) {
		q, err := runtime.ParseParams(r)
		if err != nil {
			return nil, err
		}
		var req request.SourceAdd
		if err := runtime.DecodeJSON(r, &req, &q.Validation); err != nil {
			return nil, err
		}
		if req.Confirm && !admin {
			q.Member(runtime.DetailConfirmAdminOnly, "confirm")
		}
		if err := q.Err(); err != nil {
			return nil, err
		}
		added, err := a.ctl.QueueSourceAdd(r.Context(), req.URL, convert.AddSourceOptions(req))
		if err != nil {
			return nil, err
		}
		if added.Added != nil {
			return &runtime.Response{Status: http.StatusCreated, Location: route.Path(route.Source, added.Added.Source.ID),
				Body: convert.AddSourceResult(*added.Added)}, nil
		}
		return &runtime.Response{Status: http.StatusAccepted, Location: route.Path(route.SourceAdd, added.JobID),
			Body: convert.QueuedSourceAdd(added)}, nil
	}
}

// sourceAction is an admin-socket source change with no result body (204): POST
// /api/v1/sources/{id}/confirm|pause|resume and DELETE /api/v1/sources/{id}. A source not in a
// state the change allows is 409 state-conflict.
func (a *Handlers) SourceAction(op func(ctx context.Context, id int64) error) runtime.HandlerFunc {
	return func(r *http.Request) (any, error) {
		id, err := runtime.OnlyPathID(r, dto.ParamID)
		if err != nil {
			return nil, err
		}
		if err := op(r.Context(), id); err != nil {
			return nil, err
		}
		return runtime.NoContent(), nil
	}
}

// cancelSource is POST /api/v1/sources/{id}/cancel (admin socket): the source stops for good
// and its queued jobs are dropped; mirrored works are kept.
func (a *Handlers) CancelSource(r *http.Request) (any, error) {
	id, err := runtime.OnlyPathID(r, dto.ParamID)
	if err != nil {
		return nil, err
	}
	n, err := a.ctl.CancelSource(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.SourceCancelled(n), nil
}

// refreshSource is POST /api/v1/sources/{id}/refresh (admin socket), body {full?}: the
// source's next poll is due now and its failed work fetches are queued again; full also
// re-walks the listing. Only active and done_backfill sources can be refreshed (else 409).
func (a *Handlers) RefreshSource(r *http.Request) (any, error) {
	q, err := runtime.ParseParams(r)
	if err != nil {
		return nil, err
	}
	id := q.PathID(dto.ParamID)
	var req request.SourceRefresh
	if err := runtime.DecodeJSON(r, &req, &q.Validation); err != nil {
		return nil, err
	}
	if err := q.Err(); err != nil {
		return nil, err
	}
	res, err := a.ctl.RefreshSource(r.Context(), id, req.Full)
	if err != nil {
		return nil, err
	}
	return convert.SourceRefreshed(res), nil
}

// setSourceSettings is PATCH /api/v1/sources/{id} (admin socket), body {poll_interval?,
// priority?}: an absent member keeps its value. 204; a client that wants the new settings
// GETs the source.
func (a *Handlers) SetSourceSettings(r *http.Request) (any, error) {
	q, err := runtime.ParseParams(r)
	if err != nil {
		return nil, err
	}
	id := q.PathID(dto.ParamID)
	var req request.SourceSettings
	if err := runtime.DecodeJSON(r, &req, &q.Validation); err != nil {
		return nil, err
	}
	if err := q.Err(); err != nil {
		return nil, err
	}
	if err := a.ctl.SetSourceSettings(r.Context(), id, req.PollInterval.Duration(), req.Priority); err != nil {
		return nil, err
	}
	return runtime.NoContent(), nil
}
