// Package reparse rebuilds parsed data from stored raw pages with ZERO network requests.
// Use it after improving a parser: bump parse.Version, then `ao3mirror reparse`.
package reparse

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/skeleton"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

type Options struct {
	All     bool  // reparse every work, not only parser_version < parse.Version
	WorkID  int64 // only this work (implies All for it)
	DataDir string
	Logger  *slog.Logger
	// Progress, if set, is called after each batch with running totals.
	Progress func(Result)
}

// Result is a content-free summary.
type Result struct {
	Seen     int
	Reparsed int
	Skipped  int // already at the current parser version
	Failed   int
}

const batch = 200

func Run(ctx context.Context, st *store.Store, o Options) (Result, error) {
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	log = log.With("component", "reparse", "parser_version", parse.Version)
	var res Result
	if o.WorkID > 0 {
		err := one(ctx, st, o, log, o.WorkID, true, &res)
		return res, err
	}
	var after int64
	for {
		ids, err := st.RawPageWorkIDs(ctx, after, batch)
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
			if err := one(ctx, st, o, log, id, o.All, &res); err != nil {
				return res, err
			}
		}
		after = ids[len(ids)-1]
		if o.Progress != nil {
			o.Progress(res)
		}
	}
	log.Info("reparse finished", "seen", res.Seen, "reparsed", res.Reparsed, "skipped", res.Skipped, "failed", res.Failed)
	return res, nil
}

// one reparses a single work. Parse failures are counted (and skeletonized), not returned.
func one(ctx context.Context, st *store.Store, o Options, log *slog.Logger, id int64, force bool, res *Result) error {
	res.Seen++
	if !force {
		v, err := st.WorkParserVersion(ctx, id)
		if err != nil {
			return err
		}
		if v >= parse.Version {
			res.Skipped++
			return nil
		}
	}
	page, at, err := st.LatestRawPage(ctx, id)
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
		if o.DataDir != "" {
			if rel, serr := skeleton.SaveDebug(o.DataDir, "work", strconv.FormatInt(id, 10), page, time.Now()); serr == nil {
				log.Warn("reparse failed; saved debug skeleton", "work_id", id, "file", rel, "missing", rep.Missing)
			}
		}
		return nil
	}
	if _, err := st.PutWorkContent(ctx, id, w.Chapters, w.SkinCSS); err != nil {
		return err
	}
	if err := st.ReparseWork(ctx, w, at, parse.Version); err != nil {
		return err
	}
	res.Reparsed++
	return nil
}
