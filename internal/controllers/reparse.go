package controllers

import (
	"context"
	"errors"
	"log/slog"
	"strconv"

	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/skeleton"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// ReparseOptions select what Reparse rebuilds.
type ReparseOptions struct {
	All    bool  // reparse every work, not only parser_version < parse.Version
	WorkID int64 // only this work (implies All for it)
	// Progress, if set, is called after each batch with running totals.
	Progress func(ReparseResult)
}

// ReparseResult is a content-free summary.
type ReparseResult struct {
	Seen     int
	Reparsed int
	Skipped  int // already at the current parser version
	Failed   int
	// ImagesQueued counts images newly queued (already-known images are not counted again).
	ImagesQueued int
}

const reparseBatch = 200

// Reparse rebuilds parsed data from stored raw pages with ZERO network requests. Use it after
// improving a parser: bump parse.Version, then `ao3mirror reparse`. With images enabled it
// queues embedded images it finds (queuing makes no requests; the daemon fetches them later).
func (c *Controllers) Reparse(ctx context.Context, o ReparseOptions) (ReparseResult, error) {
	log := c.log.With("component", "reparse", "parser_version", parse.Version)
	var res ReparseResult
	if o.WorkID > 0 {
		err := c.reparseOne(ctx, log, o.WorkID, true, &res)
		return res, err
	}
	var after int64
	for {
		ids, err := c.store.RawPageWorkIDs(ctx, after, reparseBatch)
		if err != nil {
			return res, err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return res, err
			}
			if err := c.reparseOne(ctx, log, id, o.All, &res); err != nil {
				return res, err
			}
		}
		after = ids[len(ids)-1]
		if o.Progress != nil {
			o.Progress(res)
		}
	}
	log.Info("reparse finished", "seen", res.Seen, "reparsed", res.Reparsed, "skipped", res.Skipped, "failed", res.Failed,
		"images_queued", res.ImagesQueued)
	return res, nil
}

// reparseOne reparses a single work. Parse failures are counted (and skeletonized), not returned.
func (c *Controllers) reparseOne(ctx context.Context, log *slog.Logger, id int64, force bool, res *ReparseResult) error {
	res.Seen++
	if !force {
		v, err := c.store.WorkParserVersion(ctx, id)
		if err != nil {
			return err
		}
		if v >= parse.Version {
			res.Skipped++
			return nil
		}
	}
	page, at, err := c.store.LatestRawPage(ctx, id)
	if errors.Is(err, store.ErrNoRawPage) {
		res.Skipped++
		return nil
	}
	if err != nil {
		return err
	}
	w, rep, err := parse.ParseWork(id, page)
	log.Debug("parse report", append([]any{"work_id", id}, rep.Attrs()...)...)
	if err != nil {
		res.Failed++
		if c.options.DataDir != "" {
			if rel, serr := skeleton.SaveDebug(c.options.DataDir, "work", strconv.FormatInt(id, 10), page, c.options.Now()); serr == nil {
				log.Warn("reparse failed; saved debug skeleton", "work_id", id, "file", rel, "missing", rep.Missing)
			}
		}
		return nil
	}
	sw, err := c.storeWork(ctx, w, at)
	if err != nil {
		return err
	}
	res.ImagesQueued += sw.images
	res.Reparsed++
	return nil
}
