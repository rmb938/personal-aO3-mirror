package controllers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/jobs"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// EstBytesPerWork is a rough on-disk cost per mirrored work (zstd raw page + parsed bodies + meta).
const EstBytesPerWork = 60 << 10

var (
	ErrNotFound    = errors.New("sources: not found on AO3")
	ErrRestricted  = errors.New("sources: restricted or hidden on AO3 (login not supported yet)")
	ErrUnavailable = errors.New("sources: AO3 unavailable or rate limiting; try again later")
	ErrUnexpected  = errors.New("sources: unexpected page structure")
)

// ExistsError reports a duplicate add.
type ExistsError struct{ ID int64 }

func (e ExistsError) Error() string { return fmt.Sprintf("sources: already added as source %d", e.ID) }

// AddSourceOptions tune a new source; zero values use config defaults.
type AddSourceOptions struct {
	Order        string        // e.g. "kudos_desc"
	Priority     int           // higher is picked first within a class
	PollInterval time.Duration // update poll interval
	Confirm      bool          // skip the auto_confirm_max gate

	queuedAdd int64 // source_adds row to drop in the same commit (AddQueuedSource)
}

// Estimate is the projected cost of a full backfill. It contains no names.
type Estimate struct {
	Total           int
	Pages           int
	ListingRequests int // remaining listing pages (page 1 already fetched)
	WorkFetches     int
	Requests        int
	Duration        time.Duration
	DiskBytes       int64
	Windowed        bool // >100k results: backfill sliced into revised_at date windows
}

type AddSourceResult struct {
	Source   store.SourceRow
	Estimate Estimate
	Blurbs   int // blurbs stored from page 1
}

// AddSource parses url, fetches page 1 once (for listing sources), stores its blurbs, estimates the
// backfill and inserts the source as active or pending_confirm. Page-1 works that need a fetch
// are queued; they only run once the source is active (the scheduler skips jobs of pending or
// paused sources).
//
// Everything after the fetch is one transaction: a failure leaves no source and no page-1 data
// behind.
//
// For a listing source this makes an AO3 request, so it only runs in the daemon (the
// add_source job, see AddQueuedSource); the CLI queues adds with QueueSourceAdd.
func (c *Controllers) AddSource(ctx context.Context, rawURL string, o AddSourceOptions) (AddSourceResult, error) {
	src, err := ao3url.Parse(rawURL)
	if err != nil {
		return AddSourceResult{}, err
	}
	orderName := o.Order
	if orderName == "" {
		orderName = c.options.Sources.DefaultBackfillOrder
	}
	order, err := ao3url.ParseOrder(orderName)
	if err != nil {
		return AddSourceResult{}, err
	}
	poll := o.PollInterval
	if poll == 0 {
		poll = c.options.Sources.PollInterval
	}
	filters := ao3url.FiltersJSON(src.Filters)
	base, err := ao3url.BasePath(src)
	if err != nil {
		return AddSourceResult{}, err
	}
	log := c.log.With("component", "sources", "kind", string(src.Kind), "target", httpx.Hash(src.Target), "filters", len(src.Filters))

	var page1 *parsedListing
	var total int
	if src.Kind != ao3url.KindWork {
		// Check for a duplicate before spending a request on page 1.
		existing, err := store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) (int64, error) {
			return tx.FindSource(ctx, string(src.Kind), src.Target, filters)
		})
		if err != nil {
			return AddSourceResult{}, err
		}
		if existing != 0 {
			return AddSourceResult{}, ExistsError{ID: existing}
		}
		p, t, err := c.addFirstPage(ctx, src, order)
		if err != nil {
			return AddSourceResult{}, err
		}
		page1, total = &p, t
	}

	row := store.SourceRow{
		Kind: string(src.Kind), Target: src.Target, FiltersJSON: filters, Path: base,
		BackfillOrder: orderName, PollInterval: poll, Priority: o.Priority,
		NextPollAt: c.options.Now().Add(poll),
	}
	var res AddSourceResult
	err = c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		var blurbs blurbsResult
		if page1 == nil {
			// Single work: no listing; the scheduler fetches it (1 request).
			res.Estimate = c.estimate(1, 0, 1, false)
			row.State = store.SourceActive
			row.TotalWorks, row.EstRequests = 1, res.Estimate.Requests
			row.BackfillCursor = SourceCursor{NextPage: 0, Order: orderName}.JSON()
		} else {
			var err error
			if blurbs, err = c.storeListing(ctx, tx, *page1, 1); err != nil {
				return err
			}
			res.Blurbs = len(blurbs.ids)
			// Windowed walks cover every work; others only reach the first MaxResults.
			windowed := c.needsWindows(src.Kind, total)
			reachable := total
			if !windowed {
				reachable = min(total, c.options.MaxResults)
			}
			pages := ao3url.Pages(total)
			listing := max(ao3url.Pages(reachable)-1, 0)
			// Works on page 1 that are already mirrored and unchanged need no fetch.
			fetches := max(reachable-(len(blurbs.ids)-len(blurbs.needFetch)), 0)
			res.Estimate = c.estimate(total, listing, fetches, windowed)
			res.Estimate.Pages = ao3url.Pages(reachable)
			row.TotalWorks, row.EstRequests = total, res.Estimate.Requests
			next := 2
			if pages <= 1 {
				next = 0
			}
			row.BackfillCursor = SourceCursor{NextPage: next, Order: orderName}.JSON()
			if o.Confirm || total <= c.options.Sources.AutoConfirmMax {
				row.State = store.SourceActive
			} else {
				row.State = store.SourcePendingConfirm
			}
		}
		id, err := tx.InsertSource(ctx, row)
		if errors.Is(err, store.ErrSourceExists) {
			existing, _ := tx.FindSource(ctx, row.Kind, row.Target, row.FiltersJSON)
			return ExistsError{ID: existing}
		}
		if err != nil {
			return err
		}
		row.ID = id
		if o.queuedAdd > 0 {
			if err := tx.DeleteSourceAdd(ctx, o.queuedAdd); err != nil {
				return err
			}
		}
		if len(blurbs.ids) > 0 {
			n, err := linkAndQueueBlurbs(ctx, tx, id, &blurbs, jobs.ClassBackfill)
			if err != nil {
				return err
			}
			log.Debug("page-1 fetches queued", "source_id", id, "jobs", n)
		}
		res.Source = row
		return nil
	})
	if err != nil {
		return AddSourceResult{}, err
	}
	log.Info("source added", "source_id", row.ID, "state", string(row.State), "total_works", row.TotalWorks,
		"est_requests", res.Estimate.Requests, "est_hours", int(res.Estimate.Duration.Hours()),
		"est_disk_mb", res.Estimate.DiskBytes>>20, "windowed", res.Estimate.Windowed,
		"auto_confirm_max", c.options.Sources.AutoConfirmMax, "page1_blurbs", res.Blurbs)
	return res, nil
}

func (c *Controllers) estimate(total, listing, fetches int, windowed bool) Estimate {
	req := listing + fetches
	return Estimate{
		Total: total, Pages: ao3url.Pages(total), ListingRequests: listing, WorkFetches: fetches,
		Requests: req, Duration: time.Duration(req) * c.options.Interval,
		DiskBytes: int64(fetches) * EstBytesPerWork, Windowed: windowed,
	}
}

// addFirstPage fetches and parses page 1 of a new source (no store access). It returns the
// parsed page and the source's total.
func (c *Controllers) addFirstPage(ctx context.Context, src ao3url.Source, order ao3url.Order) (parsedListing, int, error) {
	resp, err := c.getListing(ctx, src, 1, order, nil)
	if err != nil {
		return parsedListing{}, 0, err
	}
	switch resp.Class {
	case httpx.ClassOK:
	case httpx.ClassNotFound:
		return parsedListing{}, 0, ErrNotFound
	case httpx.ClassRestricted, httpx.ClassHidden:
		return parsedListing{}, 0, ErrRestricted
	case httpx.ClassRateLimited, httpx.ClassUnavailable:
		return parsedListing{}, 0, fmt.Errorf("%w (%s)", ErrUnavailable, resp.Reason)
	default:
		if !pageStructureProblem(resp) {
			// An odd answer (adult gate, unusual 4xx, unfollowed redirect): not our parser.
			return parsedListing{}, 0, PermanentError{Code: CodeOddResponse, Detail: resp.Reason}
		}
		c.saveSkeleton(ctx, "listing", skeletonAddID(src), resp.Body)
		return parsedListing{}, 0, fmt.Errorf("%w (%s)", ErrUnexpected, resp.Reason)
	}
	p, err := c.parseListing(ctx, src, 1, resp)
	if err != nil {
		c.saveSkeleton(ctx, "listing", skeletonAddID(src), resp.Body)
		return p, 0, fmt.Errorf("%w: %w", ErrUnexpected, err)
	}
	total := p.listing.Total
	if total < 0 {
		if p.listing.Pagination.Last > 1 {
			return p, 0, fmt.Errorf("%w: listing total not found", ErrUnexpected)
		}
		total = len(p.listing.Blurbs)
	}
	return p, total, nil
}

// skeletonAddID names the debug skeleton of a source's page 1 at add time (no source id yet).
func skeletonAddID(src ao3url.Source) string { return "add-" + sourceHash(src) + "-p1" }

// sourceHash is a content-free identity of a source (kind, target and filters, the same triple
// sources are unique on), used where no source id exists yet: the add dedupe key and debug
// skeleton names.
func sourceHash(src ao3url.Source) string {
	return httpx.Hash(string(src.Kind) + "\x00" + src.Target + "\x00" + ao3url.FiltersJSON(src.Filters))
}

// needsWindows is the one rule for walking a source in date windows: AO3 only serves the first
// MaxResults of a listing, so a sortable source bigger than that is walked window by window.
// The add estimate and the backfill both use it.
func (c *Controllers) needsWindows(kind ao3url.Kind, total int) bool {
	return kind.Sortable() && total > c.options.MaxResults
}

// ConfirmSource activates a pending source.
func (c *Controllers) ConfirmSource(ctx context.Context, id int64) error {
	return c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error { return confirmSource(ctx, tx, id) })
}

func confirmSource(ctx context.Context, tx *store.Tx, id int64) error {
	return tx.SetSourceState(ctx, id, store.SourceActive, store.SourcePendingConfirm)
}

// PauseSource stops scheduling for a source; queued jobs stay queued but are not picked.
func (c *Controllers) PauseSource(ctx context.Context, id int64) error {
	return c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error { return pauseSource(ctx, tx, id) })
}

func pauseSource(ctx context.Context, tx *store.Tx, id int64) error {
	return tx.SetSourceState(ctx, id, store.SourcePaused, store.SourceActive, store.SourceDoneBackfill, store.SourcePendingConfirm)
}

// ResumeSource re-activates a paused source.
func (c *Controllers) ResumeSource(ctx context.Context, id int64) error {
	return c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error { return resumeSource(ctx, tx, id) })
}

func resumeSource(ctx context.Context, tx *store.Tx, id int64) error {
	return tx.SetSourceState(ctx, id, store.SourceActive, store.SourcePaused)
}

// CancelSource stops a source for good and drops its queued jobs. Mirrored works are kept. It
// returns how many queued jobs were dropped.
func (c *Controllers) CancelSource(ctx context.Context, id int64) (int64, error) {
	var n int64
	err := c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		var err error
		n, err = cancelSource(ctx, tx, id)
		return err
	})
	return n, err
}

func cancelSource(ctx context.Context, tx *store.Tx, id int64) (int64, error) {
	if err := tx.SetSourceState(ctx, id, store.SourceCancelled,
		store.SourcePendingConfirm, store.SourceActive, store.SourcePaused, store.SourceDoneBackfill); err != nil {
		return 0, err
	}
	return tx.CancelSourceJobs(ctx, id)
}

// RefreshSourceResult says what a refresh scheduled (content-free).
type RefreshSourceResult struct {
	PollNow         bool
	RequeuedWorks   int
	RescanStarted   bool
	UnblockedParser bool
}

// RefreshSource makes a source catch up now instead of waiting: its next update poll is due
// immediately and its failed work fetches are queued again. With full, the whole listing is
// re-walked (a rescan, picking up anything missed) and a parse-failure block is cleared.
// Only active and done_backfill sources can be refreshed.
func (c *Controllers) RefreshSource(ctx context.Context, id int64, full bool) (RefreshSourceResult, error) {
	var res RefreshSourceResult
	err := c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		var err error
		res, err = c.refreshSource(ctx, tx, id, full)
		return err
	})
	if err != nil {
		return RefreshSourceResult{}, err
	}
	c.log.Info("source refresh", "component", "sources", "source_id", id, "full", full, "requeued_works", res.RequeuedWorks,
		"rescan_started", res.RescanStarted, "unblocked_parser", res.UnblockedParser)
	return res, nil
}

func (c *Controllers) refreshSource(ctx context.Context, tx *store.Tx, id int64, full bool) (RefreshSourceResult, error) {
	var res RefreshSourceResult
	r, err := tx.GetSourceRow(ctx, id)
	if err != nil {
		return res, err
	}
	if r.State != store.SourceActive && r.State != store.SourceDoneBackfill {
		return res, fmt.Errorf("sources: source %d is %s; only active or done_backfill sources can be refreshed", id, r.State)
	}
	now := c.options.Now()
	r.NextPollAt = now
	res.PollNow = true
	if full {
		cur, err := ParseSourceCursor(r.BackfillCursor)
		if err != nil {
			// Same fallback as scheduleSource: restart the walk (NextPage 0 would mean "done").
			cur = SourceCursor{NextPage: 1, Order: r.BackfillOrder}
		}
		if cur.BlockedParser != 0 {
			cur.BlockedParser = 0
			res.UnblockedParser = true
		}
		cur.RetryAt = 0
		if r.State == store.SourceDoneBackfill && r.Kind != string(ao3url.KindWork) {
			cur = rescanSourceCursor(r.BackfillOrder, now)
			res.RescanStarted = true
		}
		r.BackfillCursor = cur.JSON()
	}
	if err := tx.UpdateSourceProgress(ctx, r); err != nil {
		return res, err
	}
	if res.RescanStarted {
		if err := tx.SetSourceState(ctx, id, store.SourceActive, store.SourceDoneBackfill); err != nil {
			return res, err
		}
	}
	failed, err := tx.FailedWorkJobs(ctx, id)
	if err != nil {
		return res, err
	}
	for _, target := range failed {
		wid, _ := strconv.ParseInt(target, 10, 64)
		if _, created, err := enqueueWorkFetch(ctx, tx, wid, id, jobs.ClassPollFetch); err != nil {
			return res, err
		} else if created {
			res.RequeuedWorks++
		}
	}
	return res, nil
}

// SetSourceSettings changes a source's poll interval and/or priority. Zero/nil values keep the current one.
// A shorter poll interval takes effect at once (the next poll is moved earlier if needed).
func (c *Controllers) SetSourceSettings(ctx context.Context, id int64, poll time.Duration, priority *int) error {
	return c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error { return c.setSourceSettings(ctx, tx, id, poll, priority) })
}

func (c *Controllers) setSourceSettings(ctx context.Context, tx *store.Tx, id int64, poll time.Duration, priority *int) error {
	if poll != 0 && poll < 10*time.Minute {
		return fmt.Errorf("sources: poll interval %s must be >= 10m", poll)
	}
	r, err := tx.GetSourceRow(ctx, id)
	if err != nil {
		return err
	}
	if poll == 0 {
		poll = r.PollInterval
	}
	prio := r.Priority
	if priority != nil {
		prio = *priority
	}
	if err := tx.UpdateSourceSettings(ctx, id, poll, prio); err != nil {
		return err
	}
	if next := c.options.Now().Add(poll); !r.NextPollAt.IsZero() && next.Before(r.NextPollAt) {
		r.NextPollAt = next
		return tx.UpdateSourceProgress(ctx, r)
	}
	return nil
}

// RemoveSource deletes a source (and its memberships). Mirrored works are kept.
func (c *Controllers) RemoveSource(ctx context.Context, id int64) error {
	return c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error { return removeSource(ctx, tx, id) })
}

func removeSource(ctx context.Context, tx *store.Tx, id int64) error { return tx.DeleteSource(ctx, id) }
