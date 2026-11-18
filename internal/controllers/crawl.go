package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/jobs"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// dateFloor is when AO3 opened; backfill windows stop here.
var dateFloor = time.Date(2008, 9, 1, 0, 0, 0, 0, time.UTC)

// SourceCursor is the backfill position stored in sources.backfill_cursor (JSON).
type SourceCursor struct {
	NextPage int    `json:"next_page"` // 0 = listing backfill done
	Order    string `json:"order"`
	// Date windows on revised_at, used when a source exceeds AO3's 100k result window.
	// Backfill walks windows backwards in time: [WindowFrom, WindowTo].
	WindowFrom string `json:"window_from,omitempty"` // YYYY-MM-DD
	WindowTo   string `json:"window_to,omitempty"`
	SpanDays   int    `json:"span_days,omitempty"`
	// Rescan marks a periodic full re-walk (stats refresh + deletion detection) started at
	// RescanStarted (unix seconds). Works not seen since then get a verification fetch.
	Rescan        bool  `json:"rescan,omitempty"`
	RescanStarted int64 `json:"rescan_started,omitempty"`
	// RescanListed and RescanSkipped count blurbs stored and blurbs skipped (no work link)
	// during the rescan (a page redone after a window shrink counts again). RescanIncomplete
	// marks a walk that ended without reaching every work (see markIncompleteWalk). They show
	// whether the walk saw the whole source, and so whether its misses count (see
	// rescanSawSource).
	RescanListed     int  `json:"rescan_listed,omitempty"`
	RescanSkipped    int  `json:"rescan_skipped,omitempty"`
	RescanIncomplete bool `json:"rescan_incomplete,omitempty"`
	// BlockedParser is set to parse.Version when the NextPage listing failed to parse. Backfill
	// pauses until the binary ships a different parser version.
	BlockedParser int `json:"blocked_parser,omitempty"`
	// RetryAt (unix seconds) holds the NextPage listing back after a permanent failure that is
	// not a parse failure (an odd AO3 answer, an oversized page): it is tried again then,
	// instead of every scheduler round.
	RetryAt int64 `json:"retry_at,omitempty"`
}

func (c SourceCursor) JSON() string {
	b, _ := json.Marshal(c)
	return string(b)
}

func ParseSourceCursor(s string) (SourceCursor, error) {
	var c SourceCursor
	if s == "" || s == "{}" {
		return c, nil
	}
	err := json.Unmarshal([]byte(s), &c)
	return c, err
}

// rescanSourceCursor starts a full re-walk of a source's listing at page 1.
func rescanSourceCursor(order string, now time.Time) SourceCursor {
	return SourceCursor{NextPage: 1, Order: order, Rescan: true, RescanStarted: now.Unix()}
}

func sourceOf(r model.Source) (ao3url.Source, error) {
	f, err := ao3url.ParseFiltersJSON(r.FiltersJSON)
	if err != nil {
		return ao3url.Source{}, err
	}
	return ao3url.Source{Kind: ao3url.Kind(r.Kind), Target: r.Target, Filters: f}, nil
}

func day(t time.Time) string { return t.UTC().Format("2006-01-02") }

// readSource loads a source and its listing description for a crawl step. A missing source is
// permanent; other store errors are returned as-is (retried).
func (c *Controllers) readSource(ctx context.Context, sourceID int64) (model.Source, ao3url.Source, error) {
	r, err := store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) (model.Source, error) {
		return tx.GetSourceRow(ctx, sourceID)
	})
	if errors.Is(err, store.ErrNoSource) {
		return r, ao3url.Source{}, PermanentError{Code: CodeNoSource}
	}
	if err != nil {
		return r, ao3url.Source{}, err
	}
	src, err := sourceOf(r)
	if err != nil {
		return r, src, PermanentError{Code: CodeBadFilters}
	}
	return r, src, nil
}

// fetchSourcePage fetches and parses one listing page of a source. It touches no store. A page AO3
// served but we could not parse saves a debug skeleton and returns a parse failure.
func (c *Controllers) fetchSourcePage(ctx context.Context, sourceID int64, src ao3url.Source,
	page int, order ao3url.Order, win *ao3url.Window) (parsedListing, string, error) {
	resp, err := c.getListing(ctx, src, page, order, win)
	if err != nil {
		return parsedListing{}, "", err
	}
	skelID := strconv.FormatInt(sourceID, 10) + "-p" + strconv.Itoa(page)
	if err := outcome(resp); err != nil {
		return parsedListing{}, resp.ReqID, err // an odd answer is not a listing page: no skeleton
	}
	p, err := c.parseListing(ctx, src, page, resp)
	if err != nil {
		c.saveSkeleton(ctx, "listing", skelID, resp.Body)
		return p, resp.ReqID, parseUnexpected(p.report.Missing)
	}
	return p, resp.ReqID, nil
}

// recordPageFailure records a failed listing page on its source in one transaction: LastError,
// plus whatever update changes on the freshly read row (a cursor block, the poll time). update
// decides itself whether the cursor moved during the fetch. A source removed meanwhile is not
// an error: there is nothing left to record.
func (c *Controllers) recordPageFailure(ctx context.Context, sourceID int64, msg string, update func(fresh *model.Source)) error {
	return c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		fresh, err := tx.GetSourceRow(ctx, sourceID)
		if errors.Is(err, store.ErrNoSource) {
			return nil
		}
		if err != nil {
			return err
		}
		update(&fresh)
		fresh.LastError = msg
		return tx.UpdateSourceProgress(ctx, fresh)
	})
}

// PollSourcePage walks one page of a source newest-revised-first, and queues the next page until a
// page has nothing new or changed.
//
// Steps: read the source, fetch the page, then one write transaction that re-reads the source
// and applies only this page's changes, so a refresh or settings change made during the fetch
// is kept.
func (c *Controllers) PollSourcePage(ctx context.Context, sourceID int64, page int) (Detail, error) {
	log := c.logger(ctx)
	r, src, err := c.readSource(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	// Once per poll (page 1): find member works that still need a fetch (an earlier fetch failed
	// or never ran, e.g. daemon down). The walk stops at the first unchanged page, so works
	// deeper in the listing would otherwise wait for the full rescan. Zero requests; read here,
	// before the write transaction, because it scans all of the source's memberships.
	var stale []int64
	if page == 1 {
		if stale, err = store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) ([]int64, error) {
			// Works whose fetch gave up within store.FailedFetchBackoff are skipped: nothing is
			// queued for them meanwhile, as a retry would most likely fail the same way.
			return tx.SourceWorksNeedingFetch(ctx, sourceID, c.options.Now().Add(-store.FailedFetchBackoff), 500)
		}); err != nil {
			return nil, err
		}
	}
	p, reqID, err := c.fetchSourcePage(ctx, r.ID, src, page, ao3url.UpdateOrder, nil)
	if IsParseFailure(err) {
		// No retry: the next poll is a full poll_interval away.
		uerr := c.recordPageFailure(ctx, sourceID, "poll page "+strconv.Itoa(page)+": "+err.Error(), func(fresh *model.Source) {
			fresh.LastPollAt = c.options.Now()
		})
		return nil, errors.Join(err, uerr)
	}
	if err != nil {
		return nil, err
	}
	var detail Detail
	err = c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		fresh, err := tx.GetSourceRow(ctx, sourceID)
		if errors.Is(err, store.ErrNoSource) {
			return PermanentError{Code: CodeNoSource} // removed during the fetch
		}
		if err != nil {
			return err
		}
		blurbs, err := c.storeListing(ctx, tx, p, page)
		if err != nil {
			return err
		}
		enqueued, err := linkAndQueueBlurbs(ctx, tx, fresh.ID, &blurbs, jobs.ClassPollFetch)
		if err != nil {
			return err
		}
		fresh.LastError = ""
		requeued := 0
		for _, wid := range stale { // deduplicated: a fetch queued meanwhile is not doubled
			if _, created, err := enqueueWorkFetch(ctx, tx, wid, fresh.ID, jobs.ClassPollFetch); err != nil {
				return err
			} else if created {
				requeued++
			}
		}
		l := p.listing
		pg := l.Pagination
		more := pg.Current < pg.Last && page < c.options.MaxPollPages
		// What is new *to this source*: new or changed works, and works another source (or a
		// single fetch) already mirrored but this source had not seen. Otherwise a source that
		// shares works with one polled first would stop on page 1.
		news := blurbs.newWorks + blurbs.changed + blurbs.newLinks
		switch {
		case src.Kind == ao3url.KindSeries:
			// Series pages are in part order; new parts can be anywhere, so walk them all.
		case news == 0:
			more = false // newest-first: an unchanged page means older pages are unchanged too
		}
		if capped := pg.Current < pg.Last && page >= c.options.MaxPollPages &&
			(src.Kind == ao3url.KindSeries || news > 0); capped {
			log.Warn("poll page cap reached; later changes wait for the full rescan (consider source set --poll with a shorter interval)",
				"source_id", fresh.ID, "max_poll_pages", c.options.MaxPollPages, "page", page, "last_page", pg.Last)
		}
		if page == 1 && l.Total >= 0 {
			fresh.TotalWorks = l.Total
		}
		if more {
			if _, _, err := tx.EnqueueJob(ctx, store.NewJob{Kind: jobs.KindPoll, SourceID: fresh.ID, Target: strconv.Itoa(page + 1),
				Priority: jobs.ClassPoll, DedupeKey: jobs.PollKey(fresh.ID, page+1)}); err != nil {
				return err
			}
		} else {
			fresh.LastPollAt = c.options.Now()
		}
		if err := tx.UpdateSourceProgress(ctx, fresh); err != nil {
			return err
		}
		detail = Detail{"page": page, "blurbs": len(l.Blurbs), "new": blurbs.newWorks, "changed": blurbs.changed,
			"work_jobs": enqueued, "requeued_unfetched": requeued, "continue": more, "total": l.Total, "req_id": reqID}
		return nil
	})
	return detail, err
}

// BackfillSourcePage walks one page of a source in its backfill order; >MaxResults sources go window
// by window.
//
// Steps: read the source and cursor, fetch the page, then one write transaction that re-reads
// the source. If the cursor moved during the fetch (a refresh or rescan reset it), the page's
// blurbs are still stored but the cursor is left alone.
func (c *Controllers) BackfillSourcePage(ctx context.Context, sourceID int64, page int) (Detail, error) {
	log := c.logger(ctx)
	r, src, err := c.readSource(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	cur, err := ParseSourceCursor(r.BackfillCursor)
	if err != nil {
		return nil, PermanentError{Code: CodeBadCursor}
	}
	if cur.NextPage != page {
		return Detail{"stale": true, "cursor_page": cur.NextPage}, nil // cursor moved on; nothing to do
	}
	order, err := ao3url.ParseOrder(cur.Order)
	if err != nil {
		order, _ = ao3url.ParseOrder(r.BackfillOrder)
	}

	// Enter windowed mode when the source is larger than AO3's result window.
	if cur.WindowTo == "" && c.needsWindows(src.Kind, r.TotalWorks) {
		to := c.options.Now().UTC()
		cur.SpanDays = c.options.WindowDays
		cur.WindowTo, cur.WindowFrom = day(to), day(to.AddDate(0, 0, -cur.SpanDays+1))
		cur.NextPage = 1
		page = 1
		log.Info("backfill windowed", "total", r.TotalWorks, "max_results", c.options.MaxResults, "span_days", cur.SpanDays)
	}
	var win *ao3url.Window
	if cur.WindowTo != "" {
		from, _ := time.Parse("2006-01-02", cur.WindowFrom)
		to, _ := time.Parse("2006-01-02", cur.WindowTo)
		win = &ao3url.Window{From: from, To: to}
	}

	fetchClass := jobs.ClassBackfill
	if cur.Rescan {
		fetchClass = jobs.ClassMaintenance
	}
	p, reqID, err := c.fetchSourcePage(ctx, r.ID, src, page, order, win)
	msg := func() string { return "backfill page " + strconv.Itoa(page) + ": " + err.Error() }
	if IsParseFailure(err) {
		// The same page will parse the same way: stop this source's backfill here until a new
		// parser version ships (it then resumes automatically).
		uerr := c.recordPageFailure(ctx, sourceID, msg(), func(fresh *model.Source) {
			if fresh.BackfillCursor == r.BackfillCursor { // not moved during the fetch
				cur.BlockedParser = parse.Version
				fresh.BackfillCursor = cur.JSON()
			}
		})
		log.Warn("backfill blocked on parse failure until parser update", "page", page, "parser_version", parse.Version)
		return Detail{"page": page, "blocked_parser": parse.Version}, errors.Join(err, uerr)
	}
	if _, permanent := errors.AsType[PermanentError](err); permanent && ctx.Err() == nil {
		// Not the page structure, but retrying now would get the same answer: hold this page
		// back for a poll interval instead of re-queueing it every round.
		retryAt := c.options.Now().Add(r.PollInterval)
		uerr := c.recordPageFailure(ctx, sourceID, msg(), func(fresh *model.Source) {
			if fresh.BackfillCursor == r.BackfillCursor {
				cur.RetryAt = retryAt.Unix()
				fresh.BackfillCursor = cur.JSON()
			}
		})
		log.Warn("backfill page failed; held back", "page", page, "error", err.Error(), "retry_at", retryAt)
		return Detail{"page": page, "retry_at": retryAt.Unix()}, errors.Join(err, uerr)
	}
	if err != nil {
		return nil, err
	}

	var detail Detail
	err = c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		fresh, err := tx.GetSourceRow(ctx, sourceID)
		if errors.Is(err, store.ErrNoSource) {
			return PermanentError{Code: CodeNoSource} // removed during the fetch
		}
		if err != nil {
			return err
		}
		blurbs, err := c.storeListing(ctx, tx, p, page)
		if err != nil {
			return err
		}
		enqueued, err := linkAndQueueBlurbs(ctx, tx, fresh.ID, &blurbs, fetchClass)
		if err != nil {
			return err
		}
		l := p.listing
		detail = Detail{"page": page, "blurbs": len(l.Blurbs), "new": blurbs.newWorks, "rescan": cur.Rescan,
			"work_jobs": enqueued, "total": l.Total, "req_id": reqID}
		if win != nil {
			detail["window_to"], detail["window_from"] = cur.WindowTo, cur.WindowFrom
		}
		if fresh.BackfillCursor != r.BackfillCursor {
			// Refresh/rescan moved the cursor while we fetched: keep the blurbs, not our cursor step.
			detail["stale_after_fetch"] = true
			return nil
		}
		if cur.Rescan {
			cur.RescanListed += len(blurbs.ids)
			cur.RescanSkipped += l.Skipped
		}
		c.advanceSourceCursor(&cur, page, win, l, detail, log)
		if page == 1 && win == nil && l.Total >= 0 {
			fresh.TotalWorks = l.Total
		}
		fresh.BackfillCursor = cur.JSON()
		fresh.LastError = ""
		if err := tx.UpdateSourceProgress(ctx, fresh); err != nil {
			return err
		}
		detail["next_page"] = cur.NextPage
		return nil
	})
	return detail, err
}

// advanceSourceCursor moves a backfill cursor past a successfully stored page: next page, a smaller
// window when the current one is over AO3's result window, the previous date window, or done.
func (c *Controllers) advanceSourceCursor(cur *SourceCursor, page int, win *ao3url.Window, l parse.Listing, detail Detail, log *slog.Logger) {
	cur.BlockedParser, cur.RetryAt = 0, 0
	maxPages := ao3url.Pages(c.options.MaxResults)
	pg := l.Pagination
	switch {
	case win != nil && page == 1 && l.Total > c.options.MaxResults && cur.SpanDays > 1:
		// Window still too big: halve it and redo page 1 (blurbs already stored are kept).
		cur.SpanDays /= 2
		to, _ := time.Parse("2006-01-02", cur.WindowTo)
		cur.WindowFrom = day(to.AddDate(0, 0, -cur.SpanDays+1))
		cur.NextPage = 1
		detail["shrink_span_days"] = cur.SpanDays
	case pg.Current < pg.Last && page < maxPages:
		cur.NextPage = page + 1
	case win != nil:
		// Window done: move to the previous window.
		markIncompleteWalk(cur, page, l, detail, log)
		from, _ := time.Parse("2006-01-02", cur.WindowFrom)
		to := from.AddDate(0, 0, -1)
		if to.Before(dateFloor) {
			cur.NextPage = 0
		} else {
			// Grow back toward the configured span after shrinking.
			if cur.SpanDays < c.options.WindowDays {
				cur.SpanDays = min(cur.SpanDays*2, c.options.WindowDays)
			}
			cur.WindowTo, cur.WindowFrom = day(to), day(to.AddDate(0, 0, -cur.SpanDays+1))
			cur.NextPage = 1
		}
	default:
		if pg.Current < pg.Last {
			detail["truncated_at_max_results"] = true
			log.Warn("backfill reached AO3 result window; remaining works only via windows", "page", page)
		}
		markIncompleteWalk(cur, page, l, detail, log)
		cur.NextPage = 0
	}
}

// markIncompleteWalk is called on the last page of a walk (or of a date window). It marks a
// rescan incomplete when that page shows the walk did not reach every work: pages were left
// (AO3's result window), or the heading's total is more than the pages walked can hold (the
// pagination was not parsed, so the walk stopped early). Listings without a "N Works" total
// (bookmarks) can only be checked by their pagination.
func markIncompleteWalk(cur *SourceCursor, page int, l parse.Listing, detail Detail, log *slog.Logger) {
	pg := l.Pagination
	short := l.Total >= 0 && l.Total > page*ao3url.PerPage
	if !cur.Rescan || (pg.Current >= pg.Last && !short) {
		return
	}
	cur.RescanIncomplete = true
	if short {
		detail["total_beyond_pages"] = true
		log.Warn("listing total is more than the pages walked hold; rescan misses will not be counted",
			"page", page, "page_last", pg.Last, "total", l.Total)
	}
}
