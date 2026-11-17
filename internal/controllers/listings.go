package controllers

import (
	"context"
	"strconv"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
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
	return c.get(ctx, kind, path)
}

// parseListing parses a fetched listing page. For a series source it parses the series page and,
// on page 1, stores the series metadata. parseErr is a page-structure failure (rep says what is
// missing); err is a store failure.
func (c *Controllers) parseListing(ctx context.Context, src ao3url.Source, page int, resp *httpx.Response) (
	l parse.Listing, rep *parse.Report, parseErr, err error) {
	fetched := c.options.Now()
	var ser parse.Series
	if src.Kind == ao3url.KindSeries {
		id, _ := strconv.ParseInt(src.Target, 10, 64)
		ser, rep, parseErr = parse.ParseSeries(id, resp.Body, fetched)
		l = ser.Listing
	} else {
		l, rep, parseErr = parse.ParseListing(resp.Body, fetched)
	}
	c.logger(ctx).Debug("parse report", append([]any{"req_id", resp.ReqID, "page", page}, rep.Attrs()...)...)
	if parseErr == nil && src.Kind == ao3url.KindSeries && page == 1 {
		err = c.store.UpsertSeries(ctx, ser.Series)
	}
	return l, rep, parseErr, err
}

// blurbsResult is what storing a page of listing blurbs produced.
type blurbsResult struct {
	ids       []int64 // every blurb's work id, in page order
	needFetch []int64 // works that need a full page fetch
	newWorks  int
	changed   int
}

// upsertBlurbs stores listing blurbs (metadata and stats, zero requests) and reports which works
// need a full fetch.
func (c *Controllers) upsertBlurbs(ctx context.Context, blurbs []model.Work, page int) (blurbsResult, error) {
	res := blurbsResult{ids: make([]int64, 0, len(blurbs))}
	for _, b := range blurbs {
		up, err := c.store.UpsertWork(ctx, b, model.FromBlurb, parse.Version)
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

// linkAndQueue records the blurbs as members of a source and queues the fetches they need. It
// returns how many fetch jobs were newly created.
func (c *Controllers) linkAndQueue(ctx context.Context, sourceID int64, res blurbsResult, class int) (int, error) {
	if err := c.store.LinkSourceWorks(ctx, sourceID, res.ids); err != nil {
		return 0, err
	}
	enqueued := 0
	for _, id := range res.needFetch {
		_, created, err := c.EnqueueWorkFetch(ctx, id, sourceID, class)
		if err != nil {
			return enqueued, err
		}
		if created {
			enqueued++
		}
	}
	return enqueued, nil
}
