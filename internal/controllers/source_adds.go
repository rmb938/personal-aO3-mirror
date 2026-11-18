package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/jobs"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// CodeSourceExists and CodeNoSourceAdd are the PermanentError codes of a queued add that can
// no longer run.
const (
	CodeSourceExists ErrorCode = "source_exists"
	CodeNoSourceAdd  ErrorCode = "no_source_add"
)

// QueuedSourceAdd is what QueueSourceAdd did.
type QueuedSourceAdd struct {
	JobID   int64            // the add_source job (0 when Added is set)
	Created bool             // false: the same add was already queued and JobID is that job
	Added   *AddSourceResult // single-work sources need no request and are added at once
}

// QueueSourceAdd validates a source URL and queues its add for the daemon, which fetches page 1
// and creates the source (AddSource). The CLI never talks to AO3 itself: the daemon's single
// request loop and pacer (with its backoff and breaker) cover every request. A single-work
// source needs no request and is added at once. A duplicate fails with ExistsError.
func (c *Controllers) QueueSourceAdd(ctx context.Context, rawURL string, o AddSourceOptions) (QueuedSourceAdd, error) {
	src, err := ao3url.Parse(rawURL)
	if err != nil {
		return QueuedSourceAdd{}, err
	}
	if src.Kind == ao3url.KindWork {
		res, err := c.AddSource(ctx, rawURL, o)
		if err != nil {
			return QueuedSourceAdd{}, err
		}
		return QueuedSourceAdd{Added: &res}, nil
	}
	// Fail bad input now, not in the daemon.
	if o.Order != "" {
		if _, err := ao3url.ParseOrder(o.Order); err != nil {
			return QueuedSourceAdd{}, err
		}
	}
	if _, err := ao3url.BasePath(src); err != nil {
		return QueuedSourceAdd{}, err
	}
	filters := ao3url.FiltersJSON(src.Filters)
	var q QueuedSourceAdd
	err = c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		existing, err := tx.FindSource(ctx, string(src.Kind), src.Target, filters)
		if err != nil {
			return err
		}
		if existing != 0 {
			return ExistsError{ID: existing}
		}
		addID, err := tx.InsertSourceAdd(ctx, store.SourceAdd{URL: rawURL, Order: o.Order, Priority: o.Priority,
			PollInterval: o.PollInterval, Confirm: o.Confirm})
		if err != nil {
			return err
		}
		key := jobs.SourceAddKey(sourceHash(src))
		if q.JobID, q.Created, err = tx.EnqueueJob(ctx, store.NewJob{Kind: jobs.KindSourceAdd, Target: strconv.FormatInt(addID, 10),
			Priority: jobs.ClassOnDemand, DedupeKey: key}); err != nil {
			return err
		}
		if !q.Created {
			return tx.DeleteSourceAdd(ctx, addID) // already queued: the first add wins
		}
		return nil
	})
	if err != nil {
		return QueuedSourceAdd{}, err
	}
	c.log.Info("source add queued", "component", "sources", "kind", string(src.Kind), "target", httpx.Hash(src.Target),
		"filters", len(src.Filters), "job_id", q.JobID, "created", q.Created)
	return q, nil
}

// AddQueuedSource runs a queued source add (the add_source job, in the daemon): it fetches
// page 1 and creates the source like AddSource, then drops the queued add. final says this is
// the job's last attempt, so a retryable failure drops the add instead of leaving it behind.
func (c *Controllers) AddQueuedSource(ctx context.Context, addID int64, final bool) (Detail, error) {
	a, err := store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) (store.SourceAdd, error) {
		return tx.GetSourceAdd(ctx, addID)
	})
	if errors.Is(err, store.ErrNoSourceAdd) {
		return nil, PermanentError{Code: CodeNoSourceAdd}
	}
	if err != nil {
		return nil, err
	}
	res, err := c.AddSource(ctx, a.URL, AddSourceOptions{Order: a.Order, Priority: a.Priority, PollInterval: a.PollInterval,
		Confirm: a.Confirm, queuedAdd: a.ID})
	if err == nil {
		return Detail{"source_id": res.Source.ID, "state": string(res.Source.State), "blurbs": res.Blurbs,
			"estimate": res.Estimate}, nil
	}
	opErr := sourceAddError(err)
	if ctx.Err() != nil || isWait(opErr) {
		return nil, opErr // shutdown or a wait (e.g. robots.txt unreadable): requeued, add kept
	}
	if _, permanent := errors.AsType[PermanentError](opErr); !permanent && !final {
		return nil, opErr // retried after the backoff
	}
	if derr := c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error { return tx.DeleteSourceAdd(ctx, a.ID) }); derr != nil {
		return nil, errors.Join(opErr, derr)
	}
	return nil, opErr
}

// sourceAddError maps AddSource's errors to job outcomes.
func sourceAddError(err error) error {
	if ex, ok := errors.AsType[ExistsError](err); ok {
		return PermanentError{Code: CodeSourceExists, Detail: strconv.FormatInt(ex.ID, 10)}
	}
	switch {
	case errors.Is(err, ErrUnavailable):
		return RetryError{Code: string(httpx.ClassUnavailable)}
	case errors.Is(err, ErrNotFound):
		return PermanentError{Code: CodeNotFound}
	case errors.Is(err, ErrRestricted):
		return PermanentError{Code: CodeRestricted}
	case errors.Is(err, ErrUnexpected):
		return PermanentError{Code: CodeParseUnexpected}
	}
	return err // PermanentError (too_large), store or context errors
}

// SourceAddOutcome is where a queued source add stands.
type SourceAddOutcome struct {
	State  string           // the add_source job's state (queued, running, done, failed, cancelled)
	Error  string           // the job's last error (content-free)
	Result *AddSourceResult // set once done
}

// SourceAddOutcome reports on the add_source job jobID, for a CLI waiting on the daemon.
func (c *Controllers) SourceAddOutcome(ctx context.Context, jobID int64) (SourceAddOutcome, error) {
	var out SourceAddOutcome
	err := c.store.InReadTx(ctx, func(ctx context.Context, tx *store.ReadTx) error {
		j, err := tx.GetJob(ctx, jobID)
		if err != nil {
			return err
		}
		out.State, out.Error = j.State, j.LastError
		if j.State != model.JobDone {
			return nil
		}
		evs, err := tx.JobEvents(ctx, jobID)
		if err != nil {
			return err
		}
		var d struct {
			SourceID int64    `json:"source_id"`
			Blurbs   int      `json:"blurbs"`
			Estimate Estimate `json:"estimate"`
		}
		for _, ev := range evs {
			if ev.Event == "done" {
				if err := json.Unmarshal([]byte(ev.Detail), &d); err != nil {
					return err
				}
			}
		}
		res := AddSourceResult{Blurbs: d.Blurbs, Estimate: d.Estimate}
		if res.Source, err = tx.GetSource(ctx, d.SourceID); err != nil {
			return err
		}
		out.Result = &res
		return nil
	})
	return out, err
}
