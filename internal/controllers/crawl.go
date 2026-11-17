package controllers

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/jobs"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// dateFloor is when AO3 opened; backfill windows stop here.
var dateFloor = time.Date(2008, 9, 1, 0, 0, 0, 0, time.UTC)

// Cursor is the backfill position stored in sources.backfill_cursor (JSON).
type Cursor struct {
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
	// BlockedParser is set to parse.Version when the NextPage listing failed to parse. Backfill
	// pauses until the binary ships a different parser version.
	BlockedParser int `json:"blocked_parser,omitempty"`
}

func (c Cursor) JSON() string {
	b, _ := json.Marshal(c)
	return string(b)
}

func ParseCursor(s string) (Cursor, error) {
	var c Cursor
	if s == "" || s == "{}" {
		return c, nil
	}
	err := json.Unmarshal([]byte(s), &c)
	return c, err
}

// rescanCursor starts a full re-walk of a source's listing at page 1.
func rescanCursor(order string, now time.Time) Cursor {
	return Cursor{NextPage: 1, Order: order, Rescan: true, RescanStarted: now.Unix()}
}

func sourceOf(r store.SourceRow) (ao3url.Source, error) {
	f, err := ao3url.ParseFiltersJSON(r.FiltersJSON)
	if err != nil {
		return ao3url.Source{}, err
	}
	return ao3url.Source{Kind: ao3url.Kind(r.Kind), Target: r.Target, Filters: f}, nil
}

func day(t time.Time) string { return t.UTC().Format("2006-01-02") }

// pageResult is what a listing page produced.
type pageResult struct {
	listing  parse.Listing
	newWorks int
	changed  int
	enqueued int
	reqID    string
}

// crawlPage fetches one listing page of a source, stores its blurbs, links them to the source and
// enqueues work fetches (at fetchClass) for blurbs that need one.
func (c *Controllers) crawlPage(ctx context.Context, r store.SourceRow, src ao3url.Source,
	page int, order ao3url.Order, win *ao3url.Window, fetchClass int) (pageResult, error) {
	var res pageResult
	resp, err := c.getListing(ctx, src, page, order, win)
	if err != nil {
		return res, err
	}
	res.reqID = resp.ReqID
	skelID := strconv.FormatInt(r.ID, 10) + "-p" + strconv.Itoa(page)
	if err := outcome(resp); err != nil {
		if resp.Class == httpx.ClassUnexpected {
			c.saveSkeleton(ctx, "listing", skelID, resp.Body)
		}
		return res, err
	}
	l, rep, parseErr, err := c.parseListing(ctx, src, page, resp)
	res.listing = l
	if parseErr != nil {
		c.saveSkeleton(ctx, "listing", skelID, resp.Body)
		return res, parseUnexpected(rep.Missing)
	}
	if err != nil {
		return res, err
	}
	blurbs, err := c.upsertBlurbs(ctx, l.Blurbs, page)
	if err != nil {
		return res, err
	}
	res.newWorks, res.changed = blurbs.newWorks, blurbs.changed
	res.enqueued, err = c.linkAndQueue(ctx, r.ID, blurbs, fetchClass)
	return res, err
}

// PollPage walks one page of a source newest-revised-first, and queues the next page until a
// page has nothing new or changed.
func (c *Controllers) PollPage(ctx context.Context, sourceID int64, page int) (Detail, error) {
	log := c.logger(ctx)
	r, err := c.store.GetSource(ctx, sourceID)
	if err != nil {
		return nil, PermanentError{Code: CodeNoSource}
	}
	src, err := sourceOf(r)
	if err != nil {
		return nil, PermanentError{Code: CodeBadFilters}
	}
	res, err := c.crawlPage(ctx, r, src, page, ao3url.UpdateOrder, nil, jobs.ClassPollFetch)
	if IsParseFailure(err) {
		// No retry: the next poll is a full poll_interval away.
		r.LastError = "poll page " + strconv.Itoa(page) + ": " + err.Error()
		r.LastPollAt = c.options.Now()
		if uerr := c.store.UpdateSourceProgress(ctx, r); uerr != nil {
			return nil, uerr
		}
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	r.LastError = ""
	// Once per poll (page 1): re-queue member works that still need a fetch (an earlier fetch
	// failed or never ran, e.g. daemon down). The walk below stops at the first unchanged page,
	// so works deeper in the listing would otherwise wait for the full rescan. Zero requests.
	requeued := 0
	if page == 1 {
		ids, err := c.store.SourceWorksNeedingFetch(ctx, r.ID, 500)
		if err != nil {
			return nil, err
		}
		for _, wid := range ids {
			if _, created, err := c.EnqueueWorkFetch(ctx, wid, r.ID, jobs.ClassPollFetch); err != nil {
				return nil, err
			} else if created {
				requeued++
			}
		}
	}
	p := res.listing.Pagination
	more := p.Current < p.Last && page < c.options.MaxPollPages
	switch {
	case src.Kind == ao3url.KindSeries:
		// Series pages are in part order; new parts can be anywhere, so walk them all.
	case res.newWorks+res.changed == 0:
		more = false // newest-first: an unchanged page means older pages are unchanged too
	}
	if capped := p.Current < p.Last && page >= c.options.MaxPollPages &&
		(src.Kind == ao3url.KindSeries || res.newWorks+res.changed > 0); capped {
		log.Warn("poll page cap reached; later changes wait for the full rescan (consider source set --poll with a shorter interval)",
			"source_id", r.ID, "max_poll_pages", c.options.MaxPollPages, "page", page, "last_page", p.Last)
	}
	if page == 1 && res.listing.Total >= 0 && res.listing.Total != r.TotalWorks {
		r.TotalWorks = res.listing.Total
	}
	if more {
		if _, _, err := c.store.EnqueueJob(ctx, store.Job{Kind: jobs.KindPoll, SourceID: r.ID, Target: strconv.Itoa(page + 1),
			Priority: jobs.ClassPoll, DedupeKey: jobs.PollKey(r.ID, page+1)}); err != nil {
			return nil, err
		}
	} else {
		r.LastPollAt = c.options.Now()
	}
	if err := c.store.UpdateSourceProgress(ctx, r); err != nil {
		return nil, err
	}
	return Detail{"page": page, "blurbs": len(res.listing.Blurbs), "new": res.newWorks, "changed": res.changed,
		"work_jobs": res.enqueued, "requeued_unfetched": requeued, "continue": more, "total": res.listing.Total, "req_id": res.reqID}, nil
}

// BackfillPage walks one page of a source in its backfill order; >MaxResults sources go window
// by window.
func (c *Controllers) BackfillPage(ctx context.Context, sourceID int64, page int) (Detail, error) {
	log := c.logger(ctx)
	r, err := c.store.GetSource(ctx, sourceID)
	if err != nil {
		return nil, PermanentError{Code: CodeNoSource}
	}
	src, err := sourceOf(r)
	if err != nil {
		return nil, PermanentError{Code: CodeBadFilters}
	}
	cur, err := ParseCursor(r.BackfillCursor)
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
	if cur.WindowTo == "" && src.Kind.Sortable() && r.TotalWorks > c.options.MaxResults {
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
	res, err := c.crawlPage(ctx, r, src, page, order, win, fetchClass)
	if IsParseFailure(err) {
		// The same page will parse the same way: stop this source's backfill here until a new
		// parser version ships (it then resumes automatically).
		cur.BlockedParser = parse.Version
		r.BackfillCursor = cur.JSON()
		r.LastError = "backfill page " + strconv.Itoa(page) + ": " + err.Error()
		if uerr := c.store.UpdateSourceProgress(ctx, r); uerr != nil {
			return nil, uerr
		}
		log.Warn("backfill blocked on parse failure until parser update", "page", page, "parser_version", parse.Version)
		return Detail{"page": page, "blocked_parser": parse.Version}, err
	}
	if err != nil {
		return nil, err
	}
	cur.BlockedParser = 0
	r.LastError = ""
	detail := Detail{"page": page, "blurbs": len(res.listing.Blurbs), "new": res.newWorks, "rescan": cur.Rescan,
		"work_jobs": res.enqueued, "total": res.listing.Total, "req_id": res.reqID}
	if win != nil {
		detail["window_to"], detail["window_from"] = cur.WindowTo, cur.WindowFrom
	}

	maxPages := ao3url.Pages(c.options.MaxResults)
	p := res.listing.Pagination
	switch {
	case win != nil && page == 1 && res.listing.Total > c.options.MaxResults && cur.SpanDays > 1:
		// Window still too big: halve it and redo page 1 (blurbs already stored are kept).
		cur.SpanDays /= 2
		to, _ := time.Parse("2006-01-02", cur.WindowTo)
		cur.WindowFrom = day(to.AddDate(0, 0, -cur.SpanDays+1))
		cur.NextPage = 1
		detail["shrink_span_days"] = cur.SpanDays
	case p.Current < p.Last && page < maxPages:
		cur.NextPage = page + 1
	case win != nil:
		// Window done: move to the previous window.
		from, _ := time.Parse("2006-01-02", cur.WindowFrom)
		to := from.AddDate(0, 0, -1)
		if to.Before(dateFloor) {
			cur.NextPage = 0
		} else {
			// Grow back toward the configured span after shrinking.
			if cur.SpanDays < c.options.WindowDays {
				cur.SpanDays *= 2
				if cur.SpanDays > c.options.WindowDays {
					cur.SpanDays = c.options.WindowDays
				}
			}
			cur.WindowTo, cur.WindowFrom = day(to), day(to.AddDate(0, 0, -cur.SpanDays+1))
			cur.NextPage = 1
		}
	default:
		if p.Current < p.Last {
			detail["truncated_at_max_results"] = true
			log.Warn("backfill reached AO3 result window; remaining works only via windows", "page", page)
		}
		cur.NextPage = 0
	}
	if page == 1 && win == nil && res.listing.Total >= 0 {
		r.TotalWorks = res.listing.Total
	}
	r.BackfillCursor = cur.JSON()
	if err := c.store.UpdateSourceProgress(ctx, r); err != nil {
		return nil, err
	}
	detail["next_page"] = cur.NextPage
	return detail, nil
}
