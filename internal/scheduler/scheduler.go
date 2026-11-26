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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/jobs"
	"github.com/rmb938/personal-aO3-mirror/internal/logx"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Pacer reports the AO3 client's pacing state (httpx.Client implements it).
type Pacer interface {
	PacerState() httpx.PacerState
}

type Options struct {
	Store       *store.Store // job queue, heartbeat, pruning
	Controllers *controllers.Controllers
	Pacer       Pacer
	Logger      *slog.Logger
	Now         func() time.Time
	Idle        time.Duration // sleep when nothing is runnable (default 5s)
}

// handler runs one job. Its detail is recorded as the job's event detail (content-free).
type handler func(ctx context.Context, job model.Job) (controllers.JobDetail, error)

type Scheduler struct {
	options     Options
	store       *store.Store
	controllers *controllers.Controllers
	log         *slog.Logger
	handlers    map[string]handler
	kinds       []string
	lastPrune   time.Time
	lastInfo    time.Time

	hbMu   sync.Mutex // guards curJob/phase, read by the heartbeat ticker
	curJob int64
	phase  model.DaemonPhase
}

func New(opts Options) *Scheduler {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Idle == 0 {
		opts.Idle = 5 * time.Second
	}
	s := &Scheduler{options: opts, store: opts.Store, controllers: opts.Controllers, log: opts.Logger.With("component", "scheduler")}
	s.handlers = map[string]handler{
		jobs.KindPoll: func(ctx context.Context, j model.Job) (controllers.JobDetail, error) {
			return s.controllers.PollSourcePage(ctx, j.SourceID, int(j.Target))
		},
		jobs.KindBackfill: func(ctx context.Context, j model.Job) (controllers.JobDetail, error) {
			return s.controllers.BackfillSourcePage(ctx, j.SourceID, int(j.Target))
		},
		jobs.KindWork: func(ctx context.Context, j model.Job) (controllers.JobDetail, error) {
			return s.controllers.FetchWork(ctx, j.Target, j.SourceID, j.Attempts >= j.MaxAttempts)
		},
		jobs.KindTagInfo: func(ctx context.Context, j model.Job) (controllers.JobDetail, error) {
			return s.controllers.FetchTagInfo(ctx, j.Target, j.Attempts >= j.MaxAttempts)
		},
		jobs.KindSeries: func(ctx context.Context, j model.Job) (controllers.JobDetail, error) {
			return s.controllers.FetchSeriesInfo(ctx, j.Target, j.Attempts >= j.MaxAttempts)
		},
		jobs.KindSourceAdd: func(ctx context.Context, j model.Job) (controllers.JobDetail, error) {
			return s.controllers.AddQueuedSource(ctx, j.Target, j.Attempts >= j.MaxAttempts)
		},
	}
	for k := range s.handlers {
		s.kinds = append(s.kinds, k)
	}
	return s
}

// Run loops until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) error {
	n, err := store.WriteValue(context.WithoutCancel(ctx), s.store, func(ctx context.Context, tx *store.Tx) (int64, error) {
		return tx.RecoverRunning(ctx)
	})
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
			s.setPhase(context.Background(), 0, model.DaemonStopped)
			s.log.Info("scheduler stopped")
			return nil
		}
		if err != nil {
			s.log.Error("scheduler step failed", "err", err.Error())
		}
		if !ran {
			s.setPhase(ctx, 0, model.DaemonIdle)
			select {
			case <-ctx.Done():
			case <-time.After(s.options.Idle):
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
	job, err := store.WriteValue(ctx, s.store, func(ctx context.Context, tx *store.Tx) (model.Job, error) {
		return tx.PickJob(ctx, s.kinds, s.options.Now())
	})
	if errors.Is(err, store.ErrNoJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s.execute(ctx, job)
	return true, nil
}

func (s *Scheduler) execute(ctx context.Context, job model.Job) {
	log := s.log.With("job_id", job.ID, "job_kind", job.Kind, "source_id", job.SourceID, "class", job.Priority, "attempt", job.Attempts)
	jctx := httpx.WithJobID(logx.With(ctx, log), job.ID)
	// Bookkeeping must land even when ctx is cancelled (shutdown) so a job never stays "running".
	// Each outcome (state change + its job event) is one transaction.
	bk := context.WithoutCancel(ctx)
	book := func(what string, fn func(ctx context.Context, tx *store.Tx) error) {
		if err := s.store.InTx(bk, fn); err != nil {
			log.Error("job bookkeeping failed", "step", what, "err", err.Error())
		}
	}
	s.setPhase(ctx, job.ID, model.DaemonRunning)
	book("event_start", func(ctx context.Context, tx *store.Tx) error {
		return tx.JobEvent(ctx, job.ID, model.JobEventStart, controllers.JobOutcome{Attempt: new(job.Attempts), Target: new(job.Target)})
	})
	start := s.options.Now()
	detail, err := s.runHandler(jctx, job, log)
	// out is the scheduler's part of the outcome event, stored merged with the handler's detail.
	out := controllers.JobOutcome{MS: new(s.options.Now().Sub(start).Milliseconds())}

	// retry re-queues the job (or fails it once attempts are exhausted) and records event.
	retry := func(event, code string, notBefore time.Time) string {
		var state string
		book(event, func(ctx context.Context, tx *store.Tx) error {
			var err error
			if state, err = tx.RetryJob(ctx, job.ID, notBefore, code); err != nil {
				return err
			}
			out.Error, out.NextState = new(code), new(state)
			return tx.JobEvent(ctx, job.ID, event, out, detail)
		})
		return state
	}
	finish := func(event, state, lastError string) {
		book(event, func(ctx context.Context, tx *store.Tx) error {
			if err := tx.FinishJob(ctx, job.ID, state, lastError); err != nil {
				return err
			}
			return tx.JobEvent(ctx, job.ID, event, out, detail)
		})
	}

	var re controllers.RetryError
	var pe controllers.PermanentError
	var we controllers.WaitError
	switch {
	case err == nil:
		// Checked before cancellation: a job that finished as shutdown began is done, not redone.
		finish(model.JobEventDone, model.JobDone, "")
		log.Info("job done", append([]any{"ms", *out.MS}, detailAttrs(detail)...)...)
	case ctx.Err() != nil:
		book("requeue", func(ctx context.Context, tx *store.Tx) error { return tx.RequeueJob(ctx, job.ID, s.options.Now()) })
		log.Info("job interrupted; requeued")
		return
	case errors.As(err, &we):
		// Nothing was sent (e.g. robots.txt unreadable): wait, without using up an attempt.
		nb := we.Until
		if floor := s.options.Now().Add(time.Minute); nb.Before(floor) {
			nb = floor
		}
		out.Error, out.NotBefore = new(string(we.Code)), new(nb.UTC().Truncate(time.Second))
		book("wait", func(ctx context.Context, tx *store.Tx) error {
			if err := tx.RequeueJob(ctx, job.ID, nb); err != nil {
				return err
			}
			return tx.JobEvent(ctx, job.ID, model.JobEventWait, out, detail)
		})
		log.Info("job waiting", "error", string(we.Code), "not_before", nb)
	case errors.As(err, &re):
		nb := s.options.Now().Add(time.Minute)
		if st := s.options.Pacer.PacerState(); st.BackoffTill.After(nb) {
			nb = st.BackoffTill
		}
		out.NotBefore = new(nb.UTC().Truncate(time.Second))
		state := retry(model.JobEventRetry, re.Code, nb)
		log.Warn("job retry", "error", re.Code, "next_state", state, "not_before", nb)
	case errors.As(err, &pe):
		out.Error = new(pe.Error())
		finish(model.JobEventFailed, model.JobFailed, pe.Error())
		log.Warn("job failed", "error", pe.Error())
	default:
		// Unexpected (store/transport) errors: retry; the store caps attempts.
		code := string(controllers.CodeInternal) + ":" + errCode(err)
		state := retry(model.JobEventError, code, s.options.Now().Add(5*time.Minute))
		log.Error("job error", "error", err.Error(), "next_state", state)
	}
	s.setPhase(ctx, 0, model.DaemonBetween)
}

// runHandler runs a job's handler, turning a panic into a permanent failure so one bad page or
// bug cannot take down the daemon (or crash-loop it via restart recovery). Only the panic's type
// is logged: its value could carry page content.
func (s *Scheduler) runHandler(ctx context.Context, job model.Job, log *slog.Logger) (detail controllers.JobDetail, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("job handler panicked", "panic_type", fmt.Sprintf("%T", r))
			detail, err = nil, controllers.PermanentError{Code: controllers.CodePanic}
		}
	}()
	return s.handlers[job.Kind](ctx, job)
}

// errCode keeps internal errors short and content-free (they are ours, not AO3 text). AO3
// client errors that mean something (robots disallow, too large) never get here: the
// controllers turn them into a PermanentError.
func errCode(err error) string {
	msg := err.Error()
	if len(msg) > 120 {
		msg = msg[:120]
	}
	return msg
}

// detailAttrs returns a job detail's set fields as log attributes (slog.Attr, one per field),
// keyed as stored and sorted by key (none for a nil detail).
func detailAttrs(d controllers.JobDetail) []any {
	b, err := store.JobDetailJSON(d)
	if err != nil {
		return []any{slog.String("detail_error", err.Error())}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return []any{slog.String("detail_error", err.Error())}
	}
	out := make([]any, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		out = append(out, slog.Any(k, m[k]))
	}
	return out
}

// ---- scheduling ----

// infoInterval is how often tag/series info scheduling runs. Picking what needs info scans tag
// usage, and it only has to refill small (25-job) queues, so it runs rarely.
const infoInterval = 10 * time.Minute

// schedule asks the controllers to queue due work: info fetches (at most every infoInterval) and
// each source's polls and backfill pages. The scheduler's logger rides along in ctx so
// scheduling logs keep the scheduler component.
func (s *Scheduler) schedule(ctx context.Context) error {
	ctx = logx.With(ctx, s.log)
	now := s.options.Now()
	if now.Sub(s.lastInfo) >= infoInterval {
		s.lastInfo = now
		if err := s.controllers.ScheduleInfo(ctx, now); err != nil {
			return err
		}
	}
	return s.controllers.ScheduleSources(ctx, now)
}

func (s *Scheduler) maybePrune(ctx context.Context) {
	now := s.options.Now()
	if now.Sub(s.lastPrune) < time.Hour {
		return
	}
	s.lastPrune = now
	res, err := s.controllers.PruneHistory(ctx)
	if err != nil {
		s.log.Warn("prune failed", "err", err.Error())
		return
	}
	if res.Jobs+res.Requests > 0 {
		s.log.Info("pruned", "jobs", res.Jobs, "requests", res.Requests)
	}
}

// heartbeatEvery is how often the daemon refreshes its heartbeat, also while a job is waiting
// out a backoff. A heartbeat older than controllers.DaemonStaleAfter counts as "not running".
const heartbeatEvery = 30 * time.Second

// setPhase records what the loop is doing and publishes it immediately.
func (s *Scheduler) setPhase(ctx context.Context, jobID int64, phase model.DaemonPhase) {
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
			s.safeHeartbeat(ctx)
		}
	}
}

// safeHeartbeat keeps a heartbeat bug from killing the process (it runs on its own goroutine).
func (s *Scheduler) safeHeartbeat(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("heartbeat panicked", "panic_type", fmt.Sprintf("%T", r))
		}
	}()
	s.heartbeat(ctx)
}

// heartbeat publishes daemon state for `ao3mirror status` (content-free).
func (s *Scheduler) heartbeat(ctx context.Context) {
	s.hbMu.Lock()
	jobID, phase := s.curJob, s.phase
	s.hbMu.Unlock()
	st := s.options.Pacer.PacerState()
	hb := model.Heartbeat{
		PID:          os.Getpid(),
		Phase:        phase,
		CurrentJob:   jobID,
		WaitingUntil: st.WaitingUntil,
		Interval:     st.Interval,
		SlowFactor:   st.Factor,
		ConsecFails:  st.ConsecFails,
		BreakerOpen:  st.BreakerOpen,
		DayCount:     st.DayCount,
		DailyBudget:  st.DailyBudget,
	}
	if st.BackoffTill.After(s.options.Now()) {
		hb.BackoffUntil = st.BackoffTill
	}
	if err := s.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		return tx.SetHeartbeat(ctx, hb)
	}); err != nil && ctx.Err() == nil {
		s.log.Warn("heartbeat failed", "err", err.Error())
	}
}

// RunJob runs one specific queued job now (used by `fetch-work --now` when no daemon runs).
// It returns the job's final state.
func (s *Scheduler) RunJob(ctx context.Context, id int64) (model.Job, error) {
	job, err := store.WriteValue(ctx, s.store, func(ctx context.Context, tx *store.Tx) (model.Job, error) {
		return tx.ClaimJob(ctx, id, s.options.Now())
	})
	if err != nil {
		return model.Job{}, err
	}
	s.execute(ctx, job)
	return store.ReadValue(ctx, s.store, func(ctx context.Context, tx *store.ReadTx) (model.Job, error) {
		return tx.GetJob(ctx, id)
	})
}
