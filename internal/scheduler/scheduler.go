// Package scheduler runs the mirror: a persistent job queue in meta.db, worked by one loop so
// only one AO3 request is ever in flight (pacing itself lives in internal/httpx).
//
// Each round it (1) schedules due work from sources (update polls, backfill resume, single
// works), then (2) picks one job by priority class, source priority and round-robin fairness,
// and (3) runs it. Everything survives restarts: jobs left "running" are re-queued on start.
//
// Logs and job events are content-free (ids, counts, classes, timings).
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/jobs"
	"github.com/rmb938/personal-aO3-mirror/internal/logx"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/skeleton"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Re-exported for readability inside the package.
const (
	ClassOnDemand    = jobs.ClassOnDemand
	ClassPoll        = jobs.ClassPoll
	ClassPollFetch   = jobs.ClassPollFetch
	ClassBackfill    = jobs.ClassBackfill
	ClassTagInfo     = jobs.ClassTagInfo
	ClassMaintenance = jobs.ClassMaintenance

	KindPoll     = jobs.KindPoll
	KindBackfill = jobs.KindBackfill
	KindWork     = jobs.KindWork
	KindTagInfo  = jobs.KindTagInfo
	KindSeries   = jobs.KindSeries
)

// Fetcher is the subset of httpx.Client the scheduler needs.
type Fetcher interface {
	Get(ctx context.Context, kind httpx.Kind, ref string) (*httpx.Response, error)
	PacerState() httpx.PacerState
}

type Options struct {
	Store        *store.Store
	Client       Fetcher
	Logger       *slog.Logger
	Now          func() time.Time
	DataDir      string        // for data/debug skeletons
	KeepVersions int           // content.keep_versions
	Idle         time.Duration // sleep when nothing is runnable (default 5s)
	MaxResults   int           // AO3 search window (default ao3url.MaxResults); tests lower it
	MaxPollPages int           // cap on pages walked per update poll (default 50)
	WindowDays   int           // initial date-window span for >MaxResults sources (default 365)

	TagInfoTypes   []string      // tag types to fetch wrangling info for (empty = off)
	TagInfoTTL     time.Duration // refetch tag info after this long (default 30d)
	RescanInterval time.Duration // full re-walk of done sources (default 30d)
	QueueImages    bool          // queue embedded images of fetched works for the image worker
	SeriesInfo     bool          // fetch pages of series that mirrored works belong to
	SeriesInfoTTL  time.Duration // refresh those at least this often (default 30d)
}

type Scheduler struct {
	o         Options
	st        *store.Store
	c         Fetcher
	log       *slog.Logger
	handlers  map[string]func(context.Context, store.Job, *slog.Logger) (map[string]any, error)
	kinds     []string
	lastPrune time.Time
	lastTags  time.Time

	hbMu   sync.Mutex // guards curJob/phase, read by the heartbeat ticker
	curJob int64
	phase  string
}

func New(o Options) *Scheduler {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Idle == 0 {
		o.Idle = 5 * time.Second
	}
	if o.MaxResults == 0 {
		o.MaxResults = ao3url.MaxResults
	}
	if o.MaxPollPages == 0 {
		o.MaxPollPages = 50
	}
	if o.WindowDays == 0 {
		o.WindowDays = 365
	}
	if o.KeepVersions < 1 {
		o.KeepVersions = 1
	}
	if o.TagInfoTTL == 0 {
		o.TagInfoTTL = 30 * 24 * time.Hour
	}
	if o.SeriesInfoTTL == 0 {
		o.SeriesInfoTTL = 30 * 24 * time.Hour
	}
	if o.RescanInterval == 0 {
		o.RescanInterval = 30 * 24 * time.Hour
	}
	s := &Scheduler{o: o, st: o.Store, c: o.Client, log: o.Logger.With("component", "scheduler")}
	s.handlers = map[string]func(context.Context, store.Job, *slog.Logger) (map[string]any, error){
		KindPoll:     s.handlePoll,
		KindBackfill: s.handleBackfill,
		KindWork:     s.handleWork,
		KindTagInfo:  s.handleTagInfo,
		KindSeries:   s.handleSeriesInfo,
	}
	for k := range s.handlers {
		s.kinds = append(s.kinds, k)
	}
	return s
}

// ---- outcomes ----

// retryError asks for the job to be re-queued after the client's backoff.
type retryError struct{ code string }

func (e retryError) Error() string { return e.code }

// permanentError fails the job without retry.
type permanentError struct{ code string }

func (e permanentError) Error() string { return e.code }

// Run loops until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) error {
	n, err := s.st.RecoverRunning(context.WithoutCancel(ctx))
	if err != nil {
		return err
	}
	s.log.Info("scheduler started", "recovered_jobs", n, "kinds", fmt.Sprint(s.kinds))
	hbCtx, stopHB := context.WithCancel(ctx)
	defer stopHB()
	go s.heartbeatLoop(hbCtx)
	for {
		ran, err := s.Step(ctx)
		if ctx.Err() != nil {
			stopHB()
			s.setPhase(context.Background(), 0, "stopped")
			s.log.Info("scheduler stopped")
			return nil
		}
		if err != nil {
			s.log.Error("scheduler step failed", "err", err.Error())
		}
		if !ran {
			s.setPhase(ctx, 0, "idle")
			select {
			case <-ctx.Done():
			case <-time.After(s.o.Idle):
			}
		}
	}
}

// Step schedules due work and runs at most one job. It reports whether a job ran.
func (s *Scheduler) Step(ctx context.Context) (bool, error) {
	if err := s.schedule(ctx); err != nil {
		return false, fmt.Errorf("schedule: %w", err)
	}
	s.maybePrune(ctx)
	job, err := s.st.PickJob(ctx, s.kinds, s.o.Now())
	if errors.Is(err, store.ErrNoJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s.execute(ctx, job)
	return true, nil
}

func (s *Scheduler) execute(ctx context.Context, job store.Job) {
	log := s.log.With("job_id", job.ID, "job_kind", job.Kind, "source_id", job.SourceID, "class", job.Priority, "attempt", job.Attempts)
	jctx := httpx.WithJobID(logx.With(ctx, log), job.ID)
	s.setPhase(ctx, job.ID, "running")
	_ = s.st.JobEvent(ctx, job.ID, "start", map[string]any{"attempt": job.Attempts, "target": job.Target})
	start := time.Now()
	detail, err := s.handlers[job.Kind](jctx, job, log)
	if detail == nil {
		detail = map[string]any{}
	}
	detail["ms"] = time.Since(start).Milliseconds()

	var re retryError
	var pe permanentError
	switch {
	case ctx.Err() != nil:
		_ = s.st.RequeueJob(context.Background(), job.ID, s.o.Now())
		log.Info("job interrupted; requeued")
		return
	case err == nil:
		_ = s.st.FinishJob(ctx, job.ID, store.JobDone, "")
		log.Info("job done", append([]any{"ms", detail["ms"]}, flatten(detail)...)...)
		_ = s.st.JobEvent(ctx, job.ID, "done", detail)
	case errors.As(err, &re):
		nb := s.o.Now().Add(time.Minute)
		if st := s.c.PacerState(); st.BackoffTill.After(nb) {
			nb = st.BackoffTill
		}
		state, _ := s.st.RetryJob(ctx, job.ID, nb, re.code)
		detail["error"], detail["next_state"], detail["not_before"] = re.code, state, nb.UTC().Format(time.RFC3339)
		log.Warn("job retry", "error", re.code, "next_state", state, "not_before", nb)
		_ = s.st.JobEvent(ctx, job.ID, "retry", detail)
	case errors.As(err, &pe):
		_ = s.st.FinishJob(ctx, job.ID, store.JobFailed, pe.code)
		detail["error"] = pe.code
		log.Warn("job failed", "error", pe.code)
		_ = s.st.JobEvent(ctx, job.ID, "failed", detail)
	default:
		// Unexpected (store/transport) errors: retry; the store caps attempts.
		code := "internal:" + errCode(err)
		state, _ := s.st.RetryJob(ctx, job.ID, s.o.Now().Add(5*time.Minute), code)
		detail["error"], detail["next_state"] = code, state
		log.Error("job error", "error", err.Error(), "next_state", state)
		_ = s.st.JobEvent(ctx, job.ID, "error", detail)
	}
	s.setPhase(ctx, 0, "between")
}

// errCode keeps internal errors short and content-free (they are ours, not AO3 text).
func errCode(err error) string {
	switch {
	case errors.Is(err, httpx.ErrDisallowed):
		return "robots_disallowed"
	case errors.Is(err, httpx.ErrTooLarge):
		return "too_large"
	case errors.Is(err, httpx.ErrForeign):
		return "foreign_host"
	}
	msg := err.Error()
	if len(msg) > 120 {
		msg = msg[:120]
	}
	return msg
}

func flatten(m map[string]any) []any {
	out := make([]any, 0, 2*len(m))
	for k, v := range m {
		if k != "ms" {
			out = append(out, k, v)
		}
	}
	return out
}

// classify maps an httpx response class to a handler outcome for non-OK responses.
func outcome(resp *httpx.Response) error {
	switch resp.Class {
	case httpx.ClassRateLimited, httpx.ClassUnavailable:
		return retryError{code: string(resp.Class)}
	case httpx.ClassOK:
		return nil
	}
	return permanentError{code: string(resp.Class)}
}

// ---- scheduling ----

func (s *Scheduler) schedule(ctx context.Context) error {
	rows, err := s.st.ListSources(ctx)
	if err != nil {
		return err
	}
	now := s.o.Now()
	if err := s.scheduleTagInfo(ctx, now); err != nil {
		return err
	}
	for _, r := range rows {
		if r.State != store.SourceActive && r.State != store.SourceDoneBackfill {
			continue
		}
		log := s.log.With("source_id", r.ID, "source_kind", r.Kind)
		if r.Kind == string(ao3url.KindWork) {
			if err := s.scheduleWorkSource(ctx, r, now, log); err != nil {
				return err
			}
			continue
		}
		if !r.NextPollAt.After(now) {
			if _, created, err := s.st.EnqueueJob(ctx, store.Job{Kind: KindPoll, SourceID: r.ID, Target: "1",
				Priority: ClassPoll, DedupeKey: fmt.Sprintf("poll:%d:1", r.ID)}); err != nil {
				return err
			} else if created {
				log.Info("poll scheduled", "next_poll_at", now.Add(r.PollInterval))
			}
			r.NextPollAt = now.Add(r.PollInterval)
			if err := s.st.UpdateSourceProgress(ctx, r); err != nil {
				return err
			}
		}
		if r.State == store.SourceDoneBackfill {
			if err := s.maybeStartRescan(ctx, r, now, log); err != nil {
				return err
			}
			continue
		}
		cur, err := store.ParseCursor(r.BackfillCursor)
		if err != nil {
			log.Error("bad backfill cursor; resetting", "err", err.Error())
			cur = store.Cursor{NextPage: 1, Order: r.BackfillOrder}
		}
		if cur.NextPage > 0 && cur.BlockedParser == parse.Version {
			continue // parse failure at this page; resumes when a new parser version ships
		}
		if cur.NextPage > 0 {
			class := ClassBackfill
			if cur.Rescan {
				class = ClassMaintenance
			}
			key := backfillKey(r.ID, cur)
			if _, created, err := s.st.EnqueueJob(ctx, store.Job{Kind: KindBackfill, SourceID: r.ID,
				Target: strconv.Itoa(cur.NextPage), Priority: class, DedupeKey: key}); err != nil {
				return err
			} else if created {
				log.Debug("backfill page scheduled", "page", cur.NextPage, "window_to", cur.WindowTo, "rescan", cur.Rescan)
			}
			continue
		}
		if err := s.finishWalk(ctx, r, cur, now, log); err != nil {
			return err
		}
	}
	return nil
}

// finishWalk completes a backfill or rescan: verifies works not seen during a rescan, then
// marks the source done_backfill.
func (s *Scheduler) finishWalk(ctx context.Context, r store.SourceRow, cur store.Cursor, now time.Time, log *slog.Logger) error {
	if err := s.st.SetSourceState(ctx, r.ID, store.SourceDoneBackfill, store.SourceActive); err != nil {
		return nil // state changed concurrently (paused/cancelled); try next round
	}
	verify := 0
	if cur.Rescan {
		missing, err := s.st.SourceWorksNotSeenSince(ctx, r.ID, time.Unix(cur.RescanStarted, 0))
		if err != nil {
			return err
		}
		for _, id := range missing {
			if _, created, err := s.st.EnqueueJob(ctx, store.Job{Kind: KindWork, SourceID: r.ID, Target: strconv.FormatInt(id, 10),
				Priority: ClassMaintenance, DedupeKey: jobs.WorkKey(id)}); err != nil {
				return err
			} else if created {
				verify++
			}
		}
		cur.Rescan, cur.RescanStarted = false, 0
		r.BackfillCursor = cur.JSON()
	}
	r.BackfillDoneAt, r.LastFullScanAt = now, now
	if err := s.st.UpdateSourceProgress(ctx, r); err != nil {
		return err
	}
	log.Info("source walk complete", "works_seen", r.MemberWorks, "works_fetched", r.FetchedWorks, "verify_jobs", verify)
	return nil
}

// maybeStartRescan restarts the listing walk of a done source every RescanInterval to refresh
// stats and find works that disappeared.
func (s *Scheduler) maybeStartRescan(ctx context.Context, r store.SourceRow, now time.Time, log *slog.Logger) error {
	if r.Kind == string(ao3url.KindWork) {
		return nil
	}
	if r.LastFullScanAt.IsZero() {
		r.LastFullScanAt = now
		return s.st.UpdateSourceProgress(ctx, r)
	}
	if now.Before(r.LastFullScanAt.Add(s.o.RescanInterval)) {
		return nil
	}
	cur := store.Cursor{NextPage: 1, Order: r.BackfillOrder, Rescan: true, RescanStarted: now.Unix()}
	r.BackfillCursor = cur.JSON()
	if err := s.st.UpdateSourceProgress(ctx, r); err != nil {
		return err
	}
	if err := s.st.SetSourceState(ctx, r.ID, store.SourceActive, store.SourceDoneBackfill); err != nil {
		return nil
	}
	log.Info("rescan started", "last_full_scan", r.LastFullScanAt)
	return nil
}

// scheduleTagInfo keeps a small queue of tag-info fetches (most-used tags first).
func (s *Scheduler) scheduleTagInfo(ctx context.Context, now time.Time) error {
	if now.Sub(s.lastTags) < time.Minute {
		return nil
	}
	s.lastTags = now
	if err := s.scheduleSeriesInfo(ctx, now); err != nil {
		return err
	}
	if len(s.o.TagInfoTypes) == 0 {
		return nil
	}
	active, err := s.st.CountActiveJobs(ctx, KindTagInfo)
	if err != nil || active >= 25 {
		return err
	}
	tags, err := s.st.TagsNeedingInfo(ctx, s.o.TagInfoTypes, now.Add(-s.o.TagInfoTTL), 50)
	if err != nil {
		return err
	}
	queued := 0
	for _, t := range tags {
		if _, created, err := s.st.EnqueueJob(ctx, store.Job{Kind: KindTagInfo, Target: strconv.FormatInt(t.ID, 10),
			Priority: ClassTagInfo, DedupeKey: jobs.TagKey(t.ID)}); err != nil {
			return err
		} else if created {
			queued++
		}
	}
	if queued > 0 {
		s.log.Debug("tag info scheduled", "jobs", queued)
	}
	return nil
}

// scheduleSeriesInfo keeps a small queue of series-page fetches for non-source series.
func (s *Scheduler) scheduleSeriesInfo(ctx context.Context, now time.Time) error {
	if !s.o.SeriesInfo {
		return nil
	}
	active, err := s.st.CountActiveJobs(ctx, KindSeries)
	if err != nil || active >= 25 {
		return err
	}
	ids, err := s.st.SeriesNeedingInfo(ctx, now.Add(-s.o.SeriesInfoTTL), 50)
	if err != nil {
		return err
	}
	queued := 0
	for _, id := range ids {
		if _, created, err := s.st.EnqueueJob(ctx, store.Job{Kind: KindSeries, Target: strconv.FormatInt(id, 10),
			Priority: ClassTagInfo, DedupeKey: jobs.SeriesKey(id)}); err != nil {
			return err
		} else if created {
			queued++
		}
	}
	if queued > 0 {
		s.log.Debug("series info scheduled", "jobs", queued)
	}
	return nil
}

func backfillKey(sourceID int64, c store.Cursor) string {
	return fmt.Sprintf("backfill:%d:%s:%d", sourceID, c.WindowTo, c.NextPage)
}

// scheduleWorkSource fetches a single-work source once, then refreshes it every poll interval.
func (s *Scheduler) scheduleWorkSource(ctx context.Context, r store.SourceRow, now time.Time, log *slog.Logger) error {
	id, _ := strconv.ParseInt(r.Target, 10, 64)
	_, exists, err := s.st.WorkStatus(ctx, id)
	if err != nil {
		return err
	}
	job := store.Job{Kind: KindWork, SourceID: r.ID, Target: r.Target, DedupeKey: "work:" + r.Target}
	if !exists || r.FetchedWorks == 0 {
		// Not fetched yet: keep an on-demand job queued (deduped), don't touch the poll timer.
		job.Priority = ClassOnDemand
		_, created, err := s.st.EnqueueJob(ctx, job)
		if created {
			log.Info("work source fetch scheduled", "work_id", id)
		}
		return err
	}
	if r.NextPollAt.After(now) {
		return nil
	}
	job.Priority = ClassPoll
	if _, _, err := s.st.EnqueueJob(ctx, job); err != nil {
		return err
	}
	r.NextPollAt = now.Add(r.PollInterval)
	return s.st.UpdateSourceProgress(ctx, r)
}

func (s *Scheduler) maybePrune(ctx context.Context) {
	now := s.o.Now()
	if now.Sub(s.lastPrune) < time.Hour {
		return
	}
	s.lastPrune = now
	jobs, err1 := s.st.PruneJobs(ctx, 7*24*time.Hour, 30*24*time.Hour)
	reqs, err2 := s.st.PruneRequests(ctx, 30*24*time.Hour)
	if err := errors.Join(err1, err2); err != nil {
		s.log.Warn("prune failed", "err", err.Error())
		return
	}
	if jobs+reqs > 0 {
		s.log.Info("pruned", "jobs", jobs, "requests", reqs)
	}
}

// heartbeatEvery is how often the daemon refreshes its heartbeat, also while a job is waiting
// out a backoff. `status` treats a heartbeat older than 2 minutes as "not running".
const heartbeatEvery = 30 * time.Second

// setPhase records what the loop is doing and publishes it immediately.
func (s *Scheduler) setPhase(ctx context.Context, jobID int64, phase string) {
	s.hbMu.Lock()
	s.curJob, s.phase = jobID, phase
	s.hbMu.Unlock()
	s.heartbeat(ctx)
}

// heartbeatLoop refreshes the heartbeat until ctx ends, independent of the job loop.
func (s *Scheduler) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.heartbeat(ctx)
		}
	}
}

// heartbeat publishes daemon state for `ao3mirror status` (content-free).
func (s *Scheduler) heartbeat(ctx context.Context) {
	s.hbMu.Lock()
	jobID, phase := s.curJob, s.phase
	s.hbMu.Unlock()
	st := s.c.PacerState()
	kv := map[string]string{
		"heartbeat":     s.o.Now().UTC().Format(time.RFC3339),
		"pid":           strconv.Itoa(os.Getpid()),
		"phase":         phase,
		"current_job":   strconv.FormatInt(jobID, 10),
		"waiting_until": "",
		"interval":      st.Interval.String(),
		"slow_factor":   strconv.FormatFloat(st.Factor, 'f', 2, 64),
		"consec_fails":  strconv.Itoa(st.ConsecFails),
		"breaker_open":  strconv.FormatBool(st.BreakerOpen),
		"day_count":     strconv.Itoa(st.DayCount),
		"daily_budget":  strconv.Itoa(st.DailyBudget),
		"backoff_until": "",
	}
	if st.BackoffTill.After(s.o.Now()) {
		kv["backoff_until"] = st.BackoffTill.UTC().Format(time.RFC3339)
	}
	if !st.WaitingUntil.IsZero() {
		kv["waiting_until"] = st.WaitingUntil.UTC().Format(time.RFC3339)
	}
	if err := s.st.SetDaemonState(ctx, kv); err != nil && ctx.Err() == nil {
		s.log.Warn("heartbeat failed", "err", err.Error())
	}
}

// saveSkeleton writes a structure-only copy of an unexpected page to data/debug for diagnosis.
func (s *Scheduler) saveSkeleton(kind string, id string, page []byte, log *slog.Logger) {
	if len(page) == 0 || s.o.DataDir == "" {
		return
	}
	rel, err := skeleton.SaveDebug(s.o.DataDir, kind, id, page, s.o.Now())
	if err != nil {
		log.Warn("debug skeleton failed", "err", err.Error())
		return
	}
	log.Warn("saved debug skeleton", "file", rel)
}

// RunJob runs one specific queued job now (used by `fetch-work --now` when no daemon runs).
// It returns the job's final state.
func (s *Scheduler) RunJob(ctx context.Context, id int64) (store.Job, error) {
	job, err := s.st.ClaimJob(ctx, id, s.o.Now())
	if err != nil {
		return store.Job{}, err
	}
	s.execute(ctx, job)
	return s.st.GetJob(ctx, id)
}
