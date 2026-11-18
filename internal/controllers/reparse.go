package controllers

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/logx"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
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
		// One transaction per batch read and one per work: a single transaction over the whole
		// run would hold the write lock for minutes and stall the daemon.
		ids, err := store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) ([]int64, error) {
			return tx.RawPageWorkIDs(ctx, after, reparseBatch)
		})
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

// reparseOutcome is what happened to one work.
type reparseOutcome int

const (
	reparseSkipped reparseOutcome = iota
	reparseDone
	reparseFailed
)

// reparseOne reparses a single work (no requests). Steps: read the page (read transaction),
// parse it with no transaction open (a big page takes a while, and holding the write lock would
// stall the daemon), then write in one short transaction that re-checks the stored page did not
// change meanwhile (a fetch may have stored a newer one: that one wins, and is skipped here).
// Parse failures are counted (and skeletonized), not returned. Counters change only after the
// write committed.
func (c *Controllers) reparseOne(ctx context.Context, log *slog.Logger, id int64, force bool, res *ReparseResult) error {
	type stored struct {
		page []byte
		at   time.Time
		ok   bool
	}
	cur, err := store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) (stored, error) {
		if !force {
			v, err := tx.WorkParserVersion(ctx, id)
			if err != nil || v >= parse.Version {
				return stored{}, err
			}
		}
		page, at, err := tx.LatestRawPage(ctx, id)
		if errors.Is(err, store.ErrNoRawPage) {
			return stored{}, nil
		}
		return stored{page: page, at: at, ok: err == nil}, err
	})
	if err != nil {
		return err
	}
	outcome := reparseSkipped
	images := 0
	if cur.ok {
		w, rep, perr := parse.ParseWork(id, cur.page)
		log.Debug("parse report", append([]any{"work_id", id}, rep.Attrs()...)...)
		if perr != nil {
			outcome = reparseFailed
			// Counted, not returned: one bad page must not stop the run. The parse error names a
			// CSS path only (content-free).
			log.Warn("reparse failed", "work_id", id, "err", perr.Error(), "missing", rep.Missing)
			c.saveSkeleton(logx.With(ctx, log, "work_id", id), "work", strconv.FormatInt(id, 10), cur.page)
		} else if err := c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
			_, at, err := tx.LatestRawPage(ctx, id)
			if err != nil || !at.Equal(cur.at) {
				return err // a newer page was stored while we parsed: leave it to that fetch
			}
			sw, err := c.storeWork(ctx, tx, w, true, cur.at)
			if err != nil {
				return err
			}
			outcome, images = reparseDone, sw.images
			return nil
		}); err != nil {
			return err
		}
	}
	res.Seen++
	switch outcome {
	case reparseSkipped:
		res.Skipped++
	case reparseDone:
		res.Reparsed++
		res.ImagesQueued += images
	case reparseFailed:
		res.Failed++
	}
	return nil
}
