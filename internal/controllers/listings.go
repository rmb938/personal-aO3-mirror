package controllers

import (
	"context"
	"strconv"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// getListing fetches one listing page of a source (a series source's page is the series page).
// Unsortable kinds ignore order and window.
func (c *Controllers) getListing(ctx context.Context, src ao3url.Source, page int, order ao3url.Order, win *ao3url.Window) (*httpx.Response, error) {
	if !src.Kind.Sortable() {
		order, win = ao3url.Order{}, nil
	}
	path, err := ao3url.ListingPath(src, page, order, win)
	if err != nil {
		return nil, PermanentError{Code: CodeBadSource, Detail: err.Error()}
	}
	kind := httpx.KindListing
	if src.Kind == ao3url.KindSeries {
		kind = httpx.KindSeries
	}
	return c.ao3Get(ctx, kind, path)
}

// parsedListing is one parsed listing page. series is set for page 1 of a series source (its
// metadata is stored with the page's blurbs).
type parsedListing struct {
	listing parse.Listing
	series  *model.Series
	report  *parse.Report
}

// parseListing parses a fetched listing page (a series page for series sources). It touches no
// store; an error is a page-structure failure (report.Missing says what is missing).
func (c *Controllers) parseListing(ctx context.Context, src ao3url.Source, page int, resp *httpx.Response) (parsedListing, error) {
	fetched := c.options.Now()
	var p parsedListing
	var err error
	if src.Kind == ao3url.KindSeries {
		var ser parse.Series
		id, _ := strconv.ParseInt(src.Target, 10, 64)
		ser, p.report, err = parse.ParseSeries(id, resp.Body, fetched)
		p.listing = ser.Listing
		if err == nil && page == 1 {
			p.series = &ser.Series
		}
	} else {
		p.listing, p.report, err = parse.ParseListing(resp.Body, fetched)
	}
	c.logger(ctx).Debug("parse report", append([]any{"req_id", resp.ReqID, "page", page}, p.report.Attrs()...)...)
	return p, err
}

// blurbsResult is what storing a page of listing blurbs produced.
type blurbsResult struct {
	ids       []int64 // every blurb's work id, in page order
	needFetch []int64 // works that need a full page fetch
	newWorks  int
	changed   int
}

// storeListing stores a parsed page: the series metadata (if any) and the blurbs (metadata and
// stats, zero requests). It reports which works need a full fetch.
func (c *Controllers) storeListing(ctx context.Context, tx *store.Tx, p parsedListing, page int) (blurbsResult, error) {
	if p.series != nil {
		if err := tx.UpsertSeries(ctx, *p.series); err != nil {
			return blurbsResult{}, err
		}
	}
	res := blurbsResult{ids: make([]int64, 0, len(p.listing.Blurbs))}
	for _, b := range p.listing.Blurbs {
		up, err := tx.UpsertWork(ctx, b, model.FromBlurb, parse.Version)
		if err != nil {
			return res, err
		}
		res.ids = append(res.ids, b.ID)
		if up.New {
			res.newWorks++
		} else if up.Changed {
			res.changed++
		}
		if up.NeedsFetch {
			res.needFetch = append(res.needFetch, b.ID)
			if up.FetchReason != nil {
				c.logger(ctx).Info("work needs fetch", append([]any{"work_id", b.ID, "page", page}, up.FetchReason...)...)
			}
		}
	}
	return res, nil
}

// linkAndQueueBlurbs records the blurbs as members of a source and queues the fetches they need. It
// returns how many fetch jobs were newly created.
func linkAndQueueBlurbs(ctx context.Context, tx *store.Tx, sourceID int64, res blurbsResult, class int) (int, error) {
	if err := tx.LinkSourceWorks(ctx, sourceID, res.ids); err != nil {
		return 0, err
	}
	enqueued := 0
	for _, id := range res.needFetch {
		_, created, err := enqueueWorkFetch(ctx, tx, id, sourceID, class)
		if err != nil {
			return enqueued, err
		}
		if created {
			enqueued++
		}
	}
	return enqueued, nil
}
