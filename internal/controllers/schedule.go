package controllers

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/jobs"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// ScheduleSources queues the jobs every active source needs at now: due update polls, the next
// backfill or rescan page, single-work fetches, and the end-of-walk bookkeeping. It is one
// transaction (milliseconds; no requests). Queuing is deduplicated, so calling it every
// scheduler round is cheap and idempotent.
func (c *Controllers) ScheduleSources(ctx context.Context, now time.Time) error {
	return c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		// Without member counts: this runs before every job pick, holding the write lock.
		rows, err := tx.SchedulableSources(ctx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if err := c.scheduleSource(ctx, tx, r, now, c.logger(ctx).With("source_id", r.ID, "source_kind", r.Kind)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (c *Controllers) scheduleSource(ctx context.Context, tx *store.Tx, r store.SourceRow, now time.Time, log *slog.Logger) error {
	if r.Kind == string(ao3url.KindWork) {
		return c.scheduleWorkSource(ctx, tx, r, now, log)
	}
	if !r.NextPollAt.After(now) {
		if _, created, err := tx.EnqueueJob(ctx, store.Job{Kind: jobs.KindPoll, SourceID: r.ID, Target: "1",
			Priority: jobs.ClassPoll, DedupeKey: jobs.PollKey(r.ID, 1)}); err != nil {
			return err
		} else if created {
			log.Info("poll scheduled", "next_poll_at", now.Add(r.PollInterval))
		}
		r.NextPollAt = now.Add(r.PollInterval)
		if err := tx.UpdateSourceProgress(ctx, r); err != nil {
			return err
		}
	}
	if r.State == store.SourceDoneBackfill {
		return c.maybeStartRescan(ctx, tx, r, now, log)
	}
	cur, err := ParseCursor(r.BackfillCursor)
	if err != nil {
		log.Error("bad backfill cursor; resetting", "err", err.Error())
		cur = Cursor{NextPage: 1, Order: r.BackfillOrder}
	}
	if cur.NextPage > 0 && cur.BlockedParser == parse.Version {
		return nil // parse failure at this page; resumes when a new parser version ships
	}
	if cur.NextPage > 0 {
		class := jobs.ClassBackfill
		if cur.Rescan {
			class = jobs.ClassMaintenance
		}
		if _, created, err := tx.EnqueueJob(ctx, store.Job{Kind: jobs.KindBackfill, SourceID: r.ID,
			Target: strconv.Itoa(cur.NextPage), Priority: class, DedupeKey: jobs.BackfillKey(r.ID, cur.WindowTo, cur.NextPage)}); err != nil {
			return err
		} else if created {
			log.Debug("backfill page scheduled", "page", cur.NextPage, "window_to", cur.WindowTo, "rescan", cur.Rescan)
		}
		return nil
	}
	return finishWalk(ctx, tx, r, cur, now, log)
}

// finishWalk completes a backfill or rescan: verifies works not seen during a rescan, then
// marks the source done_backfill (state, verification jobs and cursor change together).
func finishWalk(ctx context.Context, tx *store.Tx, r store.SourceRow, cur Cursor, now time.Time, log *slog.Logger) error {
	if err := tx.SetSourceState(ctx, r.ID, store.SourceDoneBackfill, store.SourceActive); err != nil {
		if errors.Is(err, store.ErrStateConflict) {
			return nil // not active any more (paused/cancelled); try next round
		}
		return err
	}
	verify := 0
	if cur.Rescan {
		missing, err := tx.SourceWorksNotSeenSince(ctx, r.ID, time.Unix(cur.RescanStarted, 0))
		if err != nil {
			return err
		}
		for _, id := range missing {
			if _, created, err := enqueueWorkFetch(ctx, tx, id, r.ID, jobs.ClassMaintenance); err != nil {
				return err
			} else if created {
				verify++
			}
		}
		cur.Rescan, cur.RescanStarted = false, 0
		r.BackfillCursor = cur.JSON()
	}
	r.BackfillDoneAt, r.LastFullScanAt = now, now
	if err := tx.UpdateSourceProgress(ctx, r); err != nil {
		return err
	}
	seen, fetched, err := tx.SourceWorkCounts(ctx, r.ID) // once per walk, so the counts are affordable here
	if err != nil {
		return err
	}
	log.Info("source walk complete", "works_seen", seen, "works_fetched", fetched, "verify_jobs", verify)
	return nil
}

// maybeStartRescan restarts the listing walk of a done source every RescanInterval to refresh
// stats and find works that disappeared.
func (c *Controllers) maybeStartRescan(ctx context.Context, tx *store.Tx, r store.SourceRow, now time.Time, log *slog.Logger) error {
	if r.LastFullScanAt.IsZero() {
		r.LastFullScanAt = now
		return tx.UpdateSourceProgress(ctx, r)
	}
	if now.Before(r.LastFullScanAt.Add(c.options.RescanInterval)) {
		return nil
	}
	r.BackfillCursor = rescanCursor(r.BackfillOrder, now).JSON()
	if err := tx.UpdateSourceProgress(ctx, r); err != nil {
		return err
	}
	if err := tx.SetSourceState(ctx, r.ID, store.SourceActive, store.SourceDoneBackfill); err != nil {
		if errors.Is(err, store.ErrStateConflict) {
			return nil // paused/cancelled: no rescan
		}
		return err
	}
	log.Info("rescan started", "last_full_scan", r.LastFullScanAt)
	return nil
}

// scheduleWorkSource fetches a single-work source once (on demand), then every poll interval.
// The decision is when a fetch was last scheduled, never how it went: a work that is deleted,
// hidden, unparseable or failing is retried once per poll interval, not every round.
func (c *Controllers) scheduleWorkSource(ctx context.Context, tx *store.Tx, r store.SourceRow, now time.Time, log *slog.Logger) error {
	id, _ := strconv.ParseInt(r.Target, 10, 64)
	class := jobs.ClassPoll
	switch {
	case r.LastPollAt.IsZero():
		class = jobs.ClassOnDemand // first fetch
	case r.NextPollAt.After(now):
		return nil
	}
	_, created, err := enqueueWorkFetch(ctx, tx, id, r.ID, class)
	if err != nil {
		return err
	}
	if created {
		log.Info("work source fetch scheduled", "work_id", id, "first", class == jobs.ClassOnDemand)
	}
	r.LastPollAt, r.NextPollAt = now, now.Add(r.PollInterval)
	return tx.UpdateSourceProgress(ctx, r)
}

// maxInfoJobs caps how many tag-info and series-info jobs are queued at once (each kind).
const maxInfoJobs = 25

// ScheduleInfo keeps small queues of series-page and tag-info fetches (most-used tags first),
// when enabled. One transaction; no requests.
func (c *Controllers) ScheduleInfo(ctx context.Context, now time.Time) error {
	return c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		if err := c.scheduleSeriesInfo(ctx, tx, now); err != nil {
			return err
		}
		return c.scheduleTagInfo(ctx, tx, now)
	})
}

func (c *Controllers) scheduleTagInfo(ctx context.Context, tx *store.Tx, now time.Time) error {
	if len(c.options.TagInfoTypes) == 0 {
		return nil
	}
	active, err := tx.CountActiveJobs(ctx, jobs.KindTagInfo)
	if err != nil || active >= maxInfoJobs {
		return err
	}
	tags, err := tx.TagsNeedingInfo(ctx, c.options.TagInfoTypes, now.Add(-c.options.TagInfoTTL), 50)
	if err != nil {
		return err
	}
	queued := 0
	for _, t := range tags {
		if _, created, err := tx.EnqueueJob(ctx, store.Job{Kind: jobs.KindTagInfo, Target: strconv.FormatInt(t.ID, 10),
			Priority: jobs.ClassTagInfo, DedupeKey: jobs.TagKey(t.ID)}); err != nil {
			return err
		} else if created {
			queued++
		}
	}
	if queued > 0 {
		c.logger(ctx).Debug("tag info scheduled", "jobs", queued)
	}
	return nil
}

// scheduleSeriesInfo queues series-page fetches for non-source series.
func (c *Controllers) scheduleSeriesInfo(ctx context.Context, tx *store.Tx, now time.Time) error {
	if !c.options.SeriesInfo {
		return nil
	}
	active, err := tx.CountActiveJobs(ctx, jobs.KindSeries)
	if err != nil || active >= maxInfoJobs {
		return err
	}
	ids, err := tx.SeriesNeedingInfo(ctx, now.Add(-c.options.SeriesInfoTTL), 50)
	if err != nil {
		return err
	}
	queued := 0
	for _, id := range ids {
		if _, created, err := tx.EnqueueJob(ctx, store.Job{Kind: jobs.KindSeries, Target: strconv.FormatInt(id, 10),
			Priority: jobs.ClassTagInfo, DedupeKey: jobs.SeriesKey(id)}); err != nil {
			return err
		} else if created {
			queued++
		}
	}
	if queued > 0 {
		c.logger(ctx).Debug("series info scheduled", "jobs", queued)
	}
	return nil
}
