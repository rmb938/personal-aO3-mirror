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
	cur, err := ParseSourceCursor(r.BackfillCursor)
	if err != nil {
		log.Error("bad backfill cursor; resetting", "err", err.Error())
		cur = SourceCursor{NextPage: 1, Order: r.BackfillOrder}
	}
	if cur.NextPage > 0 && cur.BlockedParser == parse.Version {
		return nil // parse failure at this page; resumes when a new parser version ships
	}
	if cur.NextPage > 0 && now.Unix() < cur.RetryAt {
		return nil // this page failed for another reason; held back until RetryAt
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
func finishWalk(ctx context.Context, tx *store.Tx, r store.SourceRow, cur SourceCursor, now time.Time, log *slog.Logger) error {
	if err := tx.SetSourceState(ctx, r.ID, store.SourceDoneBackfill, store.SourceActive); err != nil {
		if errors.Is(err, store.ErrStateConflict) {
			return nil // not active any more (paused/cancelled); try next round
		}
		return err
	}
	verify := 0
	if cur.Rescan {
		complete, err := rescanSawSource(ctx, tx, r.ID, cur, log)
		if err != nil {
			return err
		}
		if complete {
			if verify, err = recordRescanMisses(ctx, tx, r.ID, time.Unix(cur.RescanStarted, 0), log); err != nil {
				return err
			}
		}
		cur.Rescan, cur.RescanStarted = false, 0
		cur.RescanListed, cur.RescanSkipped, cur.RescanIncomplete = 0, 0, false
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

// recordRescanMisses counts a finished rescan's misses and queues a check fetch for each missed
// work. It returns how many check jobs were newly created.
//
// Every missed work gets one fetch: deleted/hidden marks it (it stays a member); a successful
// fetch after MissedRescansToDrop misses in a row drops the membership (the work left the
// source; see FetchWork). One miss alone can be a work that shifted into an already-walked page
// during the hours-long walk. A work whose fetch keeps giving up is dropped at the second miss
// without a request.
func recordRescanMisses(ctx context.Context, tx *store.Tx, sourceID int64, started time.Time, log *slog.Logger) (int, error) {
	check, dropped, err := tx.RecordMissedRescan(ctx, sourceID, started)
	if err != nil {
		return 0, err
	}
	if dropped > 0 {
		log.Info("unfetchable works left the source", "dropped_members", dropped)
	}
	verify := 0
	for _, id := range check {
		if _, created, err := enqueueWorkFetch(ctx, tx, id, sourceID, jobs.ClassMaintenance); err != nil {
			return verify, err
		} else if created {
			verify++
		}
	}
	return verify, nil
}

// rescanSawSource reports whether a finished rescan saw the whole source, so that its misses
// mean the works left. A walk that did not would count works it never reached as missed, and
// two such walks would drop them from the source. Such a walk counts no misses:
//   - it ended without reaching every work: pages were left at AO3's result window, or the
//     listing's total is more than the pages walked hold (pagination not parsed);
//   - more blurbs were skipped (no work link) than stored: the listing parser most likely no
//     longer finds work links (real skips, mystery and external works, are a minority);
//   - it stored no blurb at all while the source has ok members (e.g. the blurb selector no
//     longer matches). A source whose works all really left keeps them as members, and warns
//     on every rescan, rather than risk dropping works that are still in it.
//
// Works that really leave make a listing shorter, not skipped, so they still count.
func rescanSawSource(ctx context.Context, tx *store.Tx, sourceID int64, cur SourceCursor, log *slog.Logger) (bool, error) {
	counts := []any{"listed", cur.RescanListed, "skipped", cur.RescanSkipped}
	switch {
	case cur.RescanIncomplete:
		log.Warn("rescan did not reach every work; missed works not counted", counts...)
		return false, nil
	case cur.RescanSkipped > cur.RescanListed:
		log.Warn("rescan skipped more blurbs than it stored; missed works not counted", counts...)
		return false, nil
	case cur.RescanListed > 0:
		return true, nil
	}
	members, err := tx.OkSourceMembers(ctx, sourceID)
	if err != nil {
		return false, err
	}
	if members > 0 {
		log.Warn("rescan listed none of the source's works; missed works not counted", append(counts, "ok_members", members)...)
		return false, nil
	}
	return true, nil
}

// maybeStartRescan restarts the listing walk of a done source every RescanInterval to refresh
// stats and find works that disappeared.
func (c *Controllers) maybeStartRescan(ctx context.Context, tx *store.Tx, r store.SourceRow, now time.Time, log *slog.Logger) error {
	if r.LastFullScanAt.IsZero() {
		r.LastFullScanAt = now
		return tx.UpdateSourceProgress(ctx, r)
	}
	if now.Before(r.LastFullScanAt.Add(c.options.Sources.FullRescanInterval)) {
		return nil
	}
	r.BackfillCursor = rescanSourceCursor(r.BackfillOrder, now).JSON()
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
// when enabled. No requests. Two steps: picking what needs info scans series and tag usage
// (slow on a big mirror), so it runs in a read transaction that holds no write lock; only the
// enqueueing is a (short) write transaction. Enqueueing is deduplicated, so picks that went
// stale in between are harmless.
func (c *Controllers) ScheduleInfo(ctx context.Context, now time.Time) error {
	var series, tags []int64
	if err := c.store.InReadTx(ctx, func(ctx context.Context, tx *store.ReadTx) error {
		var err error
		if series, err = c.seriesNeedingInfo(ctx, tx, now); err != nil {
			return err
		}
		tags, err = c.tagsNeedingInfo(ctx, tx, now)
		return err
	}); err != nil {
		return err
	}
	if len(series) == 0 && len(tags) == 0 {
		return nil
	}
	var queuedSeries, queuedTags int
	if err := c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		var err error
		if queuedSeries, err = enqueueInfo(ctx, tx, jobs.KindSeries, series, jobs.SeriesKey); err != nil {
			return err
		}
		queuedTags, err = enqueueInfo(ctx, tx, jobs.KindTagInfo, tags, jobs.TagKey)
		return err
	}); err != nil {
		return err
	}
	if queuedSeries > 0 || queuedTags > 0 {
		c.logger(ctx).Debug("info scheduled", "series_jobs", queuedSeries, "tag_jobs", queuedTags)
	}
	return nil
}

// tagsNeedingInfo picks tags to fetch info for (most-used first) while the tag-info queue has room.
func (c *Controllers) tagsNeedingInfo(ctx context.Context, tx *store.ReadTx, now time.Time) ([]int64, error) {
	if len(c.options.TagInfoTypes) == 0 {
		return nil, nil
	}
	active, err := tx.CountActiveJobs(ctx, jobs.KindTagInfo)
	if err != nil || active >= maxInfoJobs {
		return nil, err
	}
	refs, err := tx.TagsNeedingInfo(ctx, c.options.TagInfoTypes, now.Add(-c.options.TagInfoTTL), 50)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(refs))
	for _, t := range refs {
		ids = append(ids, t.ID)
	}
	return ids, nil
}

// seriesNeedingInfo picks non-source series to fetch while the series-info queue has room.
func (c *Controllers) seriesNeedingInfo(ctx context.Context, tx *store.ReadTx, now time.Time) ([]int64, error) {
	if !c.options.SeriesInfo {
		return nil, nil
	}
	active, err := tx.CountActiveJobs(ctx, jobs.KindSeries)
	if err != nil || active >= maxInfoJobs {
		return nil, err
	}
	return tx.SeriesNeedingInfo(ctx, now.Add(-c.options.SeriesInfoTTL), 50)
}

// enqueueInfo queues info jobs of kind for ids (deduplicated by key) and returns how many are new.
func enqueueInfo(ctx context.Context, tx *store.Tx, kind string, ids []int64, key func(int64) string) (int, error) {
	queued := 0
	for _, id := range ids {
		_, created, err := tx.EnqueueJob(ctx, store.Job{Kind: kind, Target: strconv.FormatInt(id, 10),
			Priority: jobs.ClassTagInfo, DedupeKey: key(id)})
		if err != nil {
			return queued, err
		}
		if created {
			queued++
		}
	}
	return queued, nil
}
