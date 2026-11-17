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
// backfill or rescan page, single-work fetches, and the end-of-walk bookkeeping. Queuing is
// deduplicated, so calling it every scheduler round is cheap and idempotent.
func (c *Controllers) ScheduleSources(ctx context.Context, now time.Time) error {
	rows, err := c.store.ListSources(ctx)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.State != store.SourceActive && r.State != store.SourceDoneBackfill {
			continue
		}
		if err := c.scheduleSource(ctx, r, now, c.logger(ctx).With("source_id", r.ID, "source_kind", r.Kind)); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controllers) scheduleSource(ctx context.Context, r store.SourceRow, now time.Time, log *slog.Logger) error {
	if r.Kind == string(ao3url.KindWork) {
		return c.scheduleWorkSource(ctx, r, now, log)
	}
	if !r.NextPollAt.After(now) {
		if _, created, err := c.store.EnqueueJob(ctx, store.Job{Kind: jobs.KindPoll, SourceID: r.ID, Target: "1",
			Priority: jobs.ClassPoll, DedupeKey: jobs.PollKey(r.ID, 1)}); err != nil {
			return err
		} else if created {
			log.Info("poll scheduled", "next_poll_at", now.Add(r.PollInterval))
		}
		r.NextPollAt = now.Add(r.PollInterval)
		if err := c.store.UpdateSourceProgress(ctx, r); err != nil {
			return err
		}
	}
	if r.State == store.SourceDoneBackfill {
		return c.maybeStartRescan(ctx, r, now, log)
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
		if _, created, err := c.store.EnqueueJob(ctx, store.Job{Kind: jobs.KindBackfill, SourceID: r.ID,
			Target: strconv.Itoa(cur.NextPage), Priority: class, DedupeKey: jobs.BackfillKey(r.ID, cur.WindowTo, cur.NextPage)}); err != nil {
			return err
		} else if created {
			log.Debug("backfill page scheduled", "page", cur.NextPage, "window_to", cur.WindowTo, "rescan", cur.Rescan)
		}
		return nil
	}
	return c.finishWalk(ctx, r, cur, now, log)
}

// finishWalk completes a backfill or rescan: verifies works not seen during a rescan, then
// marks the source done_backfill.
func (c *Controllers) finishWalk(ctx context.Context, r store.SourceRow, cur Cursor, now time.Time, log *slog.Logger) error {
	if err := c.store.SetSourceState(ctx, r.ID, store.SourceDoneBackfill, store.SourceActive); err != nil {
		if errors.Is(err, store.ErrStateConflict) {
			return nil // state changed concurrently (paused/cancelled); try next round
		}
		return err
	}
	verify := 0
	if cur.Rescan {
		missing, err := c.store.SourceWorksNotSeenSince(ctx, r.ID, time.Unix(cur.RescanStarted, 0))
		if err != nil {
			return err
		}
		for _, id := range missing {
			if _, created, err := c.EnqueueWorkFetch(ctx, id, r.ID, jobs.ClassMaintenance); err != nil {
				return err
			} else if created {
				verify++
			}
		}
		cur.Rescan, cur.RescanStarted = false, 0
		r.BackfillCursor = cur.JSON()
	}
	r.BackfillDoneAt, r.LastFullScanAt = now, now
	if err := c.store.UpdateSourceProgress(ctx, r); err != nil {
		return err
	}
	log.Info("source walk complete", "works_seen", r.MemberWorks, "works_fetched", r.FetchedWorks, "verify_jobs", verify)
	return nil
}

// maybeStartRescan restarts the listing walk of a done source every RescanInterval to refresh
// stats and find works that disappeared.
func (c *Controllers) maybeStartRescan(ctx context.Context, r store.SourceRow, now time.Time, log *slog.Logger) error {
	if r.LastFullScanAt.IsZero() {
		r.LastFullScanAt = now
		return c.store.UpdateSourceProgress(ctx, r)
	}
	if now.Before(r.LastFullScanAt.Add(c.options.RescanInterval)) {
		return nil
	}
	r.BackfillCursor = rescanCursor(r.BackfillOrder, now).JSON()
	if err := c.store.UpdateSourceProgress(ctx, r); err != nil {
		return err
	}
	if err := c.store.SetSourceState(ctx, r.ID, store.SourceActive, store.SourceDoneBackfill); err != nil {
		if errors.Is(err, store.ErrStateConflict) {
			return nil // paused/cancelled concurrently: no rescan
		}
		return err
	}
	log.Info("rescan started", "last_full_scan", r.LastFullScanAt)
	return nil
}

// scheduleWorkSource fetches a single-work source once, then refreshes it every poll interval.
func (c *Controllers) scheduleWorkSource(ctx context.Context, r store.SourceRow, now time.Time, log *slog.Logger) error {
	id, _ := strconv.ParseInt(r.Target, 10, 64)
	_, exists, err := c.store.WorkStatus(ctx, id)
	if err != nil {
		return err
	}
	if !exists || r.FetchedWorks == 0 {
		// Not fetched yet: keep an on-demand job queued (deduped), don't touch the poll timer.
		_, created, err := c.EnqueueWorkFetch(ctx, id, r.ID, jobs.ClassOnDemand)
		if created {
			log.Info("work source fetch scheduled", "work_id", id)
		}
		return err
	}
	if r.NextPollAt.After(now) {
		return nil
	}
	if _, _, err := c.EnqueueWorkFetch(ctx, id, r.ID, jobs.ClassPoll); err != nil {
		return err
	}
	r.NextPollAt = now.Add(r.PollInterval)
	return c.store.UpdateSourceProgress(ctx, r)
}

// maxInfoJobs caps how many tag-info and series-info jobs are queued at once (each kind).
const maxInfoJobs = 25

// ScheduleInfo keeps small queues of series-page and tag-info fetches (most-used tags first),
// when enabled.
func (c *Controllers) ScheduleInfo(ctx context.Context, now time.Time) error {
	if err := c.scheduleSeriesInfo(ctx, now); err != nil {
		return err
	}
	return c.scheduleTagInfo(ctx, now)
}

func (c *Controllers) scheduleTagInfo(ctx context.Context, now time.Time) error {
	if len(c.options.TagInfoTypes) == 0 {
		return nil
	}
	active, err := c.store.CountActiveJobs(ctx, jobs.KindTagInfo)
	if err != nil || active >= maxInfoJobs {
		return err
	}
	tags, err := c.store.TagsNeedingInfo(ctx, c.options.TagInfoTypes, now.Add(-c.options.TagInfoTTL), 50)
	if err != nil {
		return err
	}
	queued := 0
	for _, t := range tags {
		if _, created, err := c.store.EnqueueJob(ctx, store.Job{Kind: jobs.KindTagInfo, Target: strconv.FormatInt(t.ID, 10),
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
func (c *Controllers) scheduleSeriesInfo(ctx context.Context, now time.Time) error {
	if !c.options.SeriesInfo {
		return nil
	}
	active, err := c.store.CountActiveJobs(ctx, jobs.KindSeries)
	if err != nil || active >= maxInfoJobs {
		return err
	}
	ids, err := c.store.SeriesNeedingInfo(ctx, now.Add(-c.options.SeriesInfoTTL), 50)
	if err != nil {
		return err
	}
	queued := 0
	for _, id := range ids {
		if _, created, err := c.store.EnqueueJob(ctx, store.Job{Kind: jobs.KindSeries, Target: strconv.FormatInt(id, 10),
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
