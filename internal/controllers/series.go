package controllers

import (
	"context"
	"strconv"

	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// FetchSeriesInfo fetches page 1 of a non-source series: description, notes, stats and
// creators. Blurbs refresh only works we already mirror (no new works are added). Everything
// the page says is stored in one transaction. final says this is the job's last attempt.
func (c *Controllers) FetchSeriesInfo(ctx context.Context, id int64, final bool) (*SeriesInfoDetail, error) {
	if id <= 0 {
		return nil, PermanentError{Code: CodeBadSeriesID}
	}
	target := strconv.FormatInt(id, 10)
	// markFetched records an attempt that produced no metadata, so the next try waits for
	// series.info_ttl instead of being queued again every minute.
	markFetched := func() error {
		return c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error { return tx.MarkSeriesFetched(ctx, id) })
	}
	resp, err := c.ao3Get(ctx, httpx.KindSeries, "/series/"+target)
	if err != nil {
		return nil, giveUpInfo(ctx, err, final, markFetched)
	}
	detail := &SeriesInfoDetail{SeriesID: new(id), Class: new(string(resp.Class)), ReqID: new(resp.ReqID)}
	switch resp.Class {
	case httpx.ClassNotFound, httpx.ClassRestricted, httpx.ClassHidden:
		return detail, markFetched()
	}
	if err := outcome(resp); err != nil {
		return detail, giveUpInfo(ctx, err, final, markFetched)
	}
	ser, rep, err := parse.ParseSeries(id, resp.Body, c.options.Now())
	c.logger(ctx).Debug("parse report", append([]any{"req_id", resp.ReqID, "series_id", id}, rep.Attrs()...)...)
	if err != nil {
		c.saveSkeleton(ctx, "series", target, resp.Body)
		if uerr := markFetched(); uerr != nil {
			return detail, uerr
		}
		return detail, parseUnexpected(rep.Missing)
	}
	// Record every work on the page in series_works, mirrored or not (ids and part numbers only,
	// no extra requests), so partial series can name the works they are missing.
	members := make([]store.SeriesMember, 0, len(ser.Listing.Blurbs))
	for _, b := range ser.Listing.Blurbs {
		m := store.SeriesMember{WorkID: b.ID}
		for _, p := range b.Series {
			if p.ID == id {
				m.Position = p.Position
			}
		}
		members = append(members, m)
	}
	refreshed := 0
	if err := c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		if err := tx.UpsertSeries(ctx, ser.Series); err != nil {
			return err
		}
		if err := tx.SetSeriesMembers(ctx, id, members); err != nil {
			return err
		}
		for _, b := range ser.Listing.Blurbs {
			known, err := tx.WorkExists(ctx, b.ID)
			if err != nil {
				return err
			}
			if !known {
				continue
			}
			if _, err := tx.UpsertWork(ctx, b, model.FromBlurb, parse.Version); err != nil {
				return err
			}
			refreshed++
		}
		// After the blurbs: the revision covered includes what this page just refreshed.
		return tx.MarkSeriesFetched(ctx, id)
	}); err != nil {
		return detail, err
	}
	detail.WorksCount, detail.KnownWorksRefreshed, detail.MembersSeen = new(ser.WorksCount), new(refreshed), new(len(members))
	return detail, nil
}
