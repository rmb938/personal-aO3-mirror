package controllers

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/jobs"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Read models for the CLI (and the Part 2 API). Each is one read-only transaction: a consistent
// snapshot that takes no write lock. All of them are content-free (ids, counts, states).

// DaemonStaleAfter is how old a heartbeat may be while the daemon still counts as running. The
// scheduler refreshes it every 30s.
const DaemonStaleAfter = 2 * time.Minute

// StatusReport is everything `ao3mirror status` shows except the sources table, which callers
// page through with ListSources (Counts.SourcesByState has the totals).
type StatusReport struct {
	Counts        store.Counts
	Daemon        model.Heartbeat
	DaemonRunning bool           // a heartbeat within DaemonStaleAfter, and not stopped
	Images        map[string]int // by status
	ImageBytes    int64          // stored image bytes
	TagInfo       TagInfoProgress
	Jobs          map[string]map[string]int // kind -> state -> count
	PartialSeries []store.PartialSeries
	RequestErrors []store.RequestRow // most recent non-ok requests
	FailedJobs    []model.Job        // most recent failed jobs
}

// TagInfoProgress counts tags qualifying for tag info (configured types, used by a mirrored work).
type TagInfoProgress struct {
	Types               []string
	Total, Fetched, Due int
}

// Status returns the daemon, queue and progress overview.
func (c *Controllers) Status(ctx context.Context) (StatusReport, error) {
	var r StatusReport
	err := c.store.InReadTx(ctx, func(ctx context.Context, tx *store.ReadTx) error {
		var err error
		if r.Counts, err = tx.Counts(ctx); err != nil {
			return err
		}
		if r.Daemon, err = tx.Heartbeat(ctx); err != nil {
			return err
		}
		r.DaemonRunning = daemonRunning(r.Daemon, c.options.Now())
		if r.Images, r.ImageBytes, err = tx.ImageCounts(ctx); err != nil {
			return err
		}
		r.TagInfo.Types = c.options.TagInfoTypes
		if r.TagInfo.Total, r.TagInfo.Fetched, r.TagInfo.Due, err = tx.TagInfoProgress(ctx, c.options.TagInfoTypes,
			c.options.Now().Add(-c.options.TagInfoTTL)); err != nil {
			return err
		}
		if r.Jobs, err = tx.JobCounts(ctx); err != nil {
			return err
		}
		if r.PartialSeries, err = tx.PartialSeriesList(ctx, 20); err != nil {
			return err
		}
		if r.RequestErrors, err = tx.RecentRequests(ctx, true, 0, "", 10); err != nil {
			return err
		}
		r.FailedJobs, err = tx.FindJobs(ctx, model.JobFilter{State: model.JobFailed, Limit: 10})
		return err
	})
	return r, err
}

// daemonRunning reports whether heartbeat h, read at now, is from a live daemon.
func daemonRunning(h model.Heartbeat, now time.Time) bool {
	return !h.At.IsZero() && now.Sub(h.At) <= DaemonStaleAfter && h.Phase != model.DaemonStopped
}

// JobDebug is the content-free trace of one job.
type JobDebug struct {
	Job      model.Job
	Events   []JobEvent
	Requests []store.RequestRow
}

// JobEvent is one decoded job trace event.
type JobEvent struct {
	Time    time.Time
	Event   string // model.JobEvent*
	Outcome JobOutcome
	// Detail is the job kind's detail (nil for an unknown kind). A start event has none of its
	// fields set.
	Detail JobDetail
}

// DebugJob returns one job with its decoded events and its requests.
func (c *Controllers) DebugJob(ctx context.Context, id int64) (JobDebug, error) {
	var d JobDebug
	err := c.store.InReadTx(ctx, func(ctx context.Context, tx *store.ReadTx) error {
		var err error
		if d.Job, err = tx.GetJob(ctx, id); err != nil {
			return err
		}
		rows, err := tx.JobEvents(ctx, id)
		if err != nil {
			return err
		}
		for _, r := range rows {
			o, det, err := DecodeJobEvent(d.Job.Kind, r.Detail)
			if err != nil {
				return fmt.Errorf("job %d event at %d: %w", id, r.Time.Unix(), err)
			}
			d.Events = append(d.Events, JobEvent{Time: r.Time, Event: r.Event, Outcome: o, Detail: det})
		}
		d.Requests, err = tx.RecentRequests(ctx, false, id, "", 20)
		return err
	})
	return d, err
}

// WorkDebug is the content-free state of one work with its fetch jobs and requests.
type WorkDebug struct {
	ID       int64
	Diag     store.WorkDiag
	Jobs     []model.Job
	Requests []store.RequestRow
}

// DebugWork returns one work's diagnostic view, its fetch jobs and its requests.
func (c *Controllers) DebugWork(ctx context.Context, id int64) (WorkDebug, error) {
	d := WorkDebug{ID: id}
	err := c.store.InReadTx(ctx, func(ctx context.Context, tx *store.ReadTx) error {
		var err error
		if d.Diag, err = tx.WorkDiag(ctx, id); err != nil {
			return err
		}
		if d.Jobs, err = tx.FindJobs(ctx, model.JobFilter{DedupeKey: jobs.WorkKey(id), Limit: 20}); err != nil {
			return err
		}
		d.Requests, err = tx.RecentRequests(ctx, false, 0, "/works/"+strconv.FormatInt(id, 10), 20)
		return err
	})
	return d, err
}

// ListSources returns one page of sources (ordered by id, with progress counts) and the total
// number of sources, read in one transaction so they agree. offset must be ≥ 0 and limit ≥ 1.
func (c *Controllers) ListSources(ctx context.Context, offset, limit int) (page []model.Source, total int, err error) {
	err = c.store.InReadTx(ctx, func(ctx context.Context, tx *store.ReadTx) error {
		var err error
		if page, err = tx.ListSources(ctx, offset, limit); err != nil {
			return err
		}
		total, err = tx.CountSources(ctx)
		return err
	})
	return page, total, err
}

// GetSource returns one source, or store.ErrNoSource.
func (c *Controllers) GetSource(ctx context.Context, id int64) (model.Source, error) {
	return store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) (model.Source, error) {
		return tx.GetSource(ctx, id)
	})
}

// ListJobs returns one page of the jobs matching f (f.Offset ≥ 0, f.Limit ≥ 1, ordered by
// f.Order) and the number of matching jobs over all pages, read in one transaction so they agree.
func (c *Controllers) ListJobs(ctx context.Context, f model.JobFilter) (page []model.Job, total int, err error) {
	err = c.store.InReadTx(ctx, func(ctx context.Context, tx *store.ReadTx) error {
		var err error
		if page, err = tx.FindJobs(ctx, f); err != nil {
			return err
		}
		total, err = tx.CountJobs(ctx, f)
		return err
	})
	return page, total, err
}

// GetJob returns one job, or store.ErrNoJob.
func (c *Controllers) GetJob(ctx context.Context, id int64) (model.Job, error) {
	return store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) (model.Job, error) {
		return tx.GetJob(ctx, id)
	})
}

// FindJobs lists jobs matching f.
func (c *Controllers) FindJobs(ctx context.Context, f model.JobFilter) ([]model.Job, error) {
	return store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) ([]model.Job, error) {
		return tx.FindJobs(ctx, f)
	})
}
