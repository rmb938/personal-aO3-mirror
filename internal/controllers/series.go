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
// creators. Blurbs refresh only works we already mirror (no new works are added).
func (c *Controllers) FetchSeriesInfo(ctx context.Context, id int64) (Detail, error) {
	if id <= 0 {
		return nil, PermanentError{Code: CodeBadSeriesID}
	}
	target := strconv.FormatInt(id, 10)
	resp, err := c.get(ctx, httpx.KindSeries, "/series/"+target)
	if err != nil {
		return nil, err
	}
	detail := Detail{"series_id": id, "class": string(resp.Class), "req_id": resp.ReqID}
	switch resp.Class {
	case httpx.ClassNotFound, httpx.ClassRestricted, httpx.ClassHidden:
		return detail, c.store.MarkSeriesFetched(ctx, id)
	}
	if err := outcome(resp); err != nil {
		if resp.Class == httpx.ClassUnexpected {
			c.saveSkeleton(ctx, "series", target, resp.Body)
			// Mark attempted so the next try waits for series.info_ttl instead of looping.
			if uerr := c.store.MarkSeriesFetched(ctx, id); uerr != nil {
				return detail, uerr
			}
		}
		return detail, err
	}
	ser, rep, err := parse.ParseSeries(id, resp.Body, c.options.Now())
	c.logger(ctx).Debug("parse report", append([]any{"req_id", resp.ReqID, "series_id", id}, rep.Attrs()...)...)
	if err != nil {
		c.saveSkeleton(ctx, "series", target, resp.Body)
		if uerr := c.store.MarkSeriesFetched(ctx, id); uerr != nil {
			return detail, uerr
		}
		return detail, parseUnexpected(rep.Missing)
	}
	if err := c.store.UpsertSeries(ctx, ser.Series); err != nil {
		return detail, err
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
	if err := c.store.SetSeriesMembers(ctx, id, members); err != nil {
		return detail, err
	}
	refreshed := 0
	for _, b := range ser.Listing.Blurbs {
		known, err := c.store.WorkExists(ctx, b.ID)
		if err != nil {
			return detail, err
		}
		if !known {
			continue
		}
		if _, err := c.store.UpsertWork(ctx, b, model.FromBlurb, parse.Version); err != nil {
			return detail, err
		}
		refreshed++
	}
	detail["works_count"], detail["known_works_refreshed"], detail["members_seen"] = ser.WorksCount, refreshed, len(members)
	return detail, nil
}
