package api

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/jobs"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Fixed ids of the seeded rows (a fresh store numbers rows from 1).
const (
	seedTagSource    int64 = 1 // every text field a sentinel
	seedSeriesSource int64 = 2
	seedDoneAdd      int64 = 1 // add_source job, done, created seedTagSource
	seedFailedAdd    int64 = 2 // add_source job, failed with a sentinel detail
	seedOtherJob     int64 = 3 // a work job (not a source add), queued
	seedBackfillJob  int64 = 4 // a backfill job of seedTagSource with a sentinel target, queued
)

// seedTime is the store clock while seeding.
var seedTime = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

// newTestControllers opens a temp store, seeds it and returns offline controllers over it.
// The store and controllers log to log, so the leak test sees their lines too.
func newTestControllers(t *testing.T, log *slog.Logger) *controllers.Controllers {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{Logger: log, Now: func() time.Time { return seedTime }})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	seed(t, st)
	cfg := config.Default()
	return controllers.New(controllers.Options{Store: st, Logger: log, Now: func() time.Time { return seedTime },
		Sources: cfg.Sources, Interval: cfg.Rate.Interval})
}

// seed fills st with the rows the route samples read.
func seed(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	cursor := controllers.SourceCursor{NextPage: 3, Order: "kudos_desc", WindowFrom: "2020-01-01", WindowTo: "2020-12-31",
		Rescan: true, RescanStarted: seedTime.Unix(), RetryAt: seedTime.Unix() + 60}
	require.NoError(t, st.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		id, err := tx.InsertSource(ctx, model.Source{
			Kind: "tag", Target: sentinel + "target", FiltersJSON: `{"` + sentinel + `filter":["` + sentinel + `value"]}`,
			Path: "/tags/" + sentinel + "path/works", State: model.SourceActive, BackfillOrder: "kudos_desc",
			PollInterval: time.Hour, TotalWorks: 40, EstRequests: -1, BackfillCursor: cursor.JSON(),
		})
		if err != nil {
			return err
		}
		require.Equal(t, seedTagSource, id)
		tag, err := tx.GetSourceRow(ctx, id)
		if err != nil {
			return err
		}
		tag.LastError, tag.LastPollAt = "poll page 2: "+sentinel+"error", seedTime
		if err := tx.UpdateSourceProgress(ctx, tag); err != nil {
			return err
		}

		id, err = tx.InsertSource(ctx, model.Source{Kind: "series", Target: "12345", FiltersJSON: "{}",
			Path: "/series/12345", State: model.SourcePendingConfirm, TotalWorks: -1, EstRequests: -1})
		if err != nil {
			return err
		}
		require.Equal(t, seedSeriesSource, id)

		id, _, err = tx.EnqueueJob(ctx, store.NewJob{Kind: jobs.KindSourceAdd, Target: "1", DedupeKey: "seed-add-1"})
		if err != nil {
			return err
		}
		require.Equal(t, seedDoneAdd, id)
		if err := tx.FinishJob(ctx, id, model.JobDone, ""); err != nil {
			return err
		}
		if err := tx.JobEvent(ctx, id, "done", map[string]any{"source_id": seedTagSource, "blurbs": 20,
			"estimate": controllers.Estimate{Total: 40, Pages: 2, ListingRequests: 1, WorkFetches: 40, Requests: 41,
				Duration: 410 * time.Second, DiskBytes: 1 << 20}}); err != nil {
			return err
		}

		id, _, err = tx.EnqueueJob(ctx, store.NewJob{Kind: jobs.KindSourceAdd, Target: "2", DedupeKey: "seed-add-2"})
		if err != nil {
			return err
		}
		require.Equal(t, seedFailedAdd, id)
		if err := tx.FinishJob(ctx, id, model.JobFailed, string(controllers.CodeNotFound)+":"+sentinel+"detail"); err != nil {
			return err
		}

		id, _, err = tx.EnqueueJob(ctx, store.NewJob{Kind: jobs.KindWork, Target: "7", DedupeKey: jobs.WorkKey(7)})
		if err != nil {
			return err
		}
		require.Equal(t, seedOtherJob, id)

		id, _, err = tx.EnqueueJob(ctx, store.NewJob{Kind: jobs.KindBackfill, SourceID: seedTagSource,
			Target: sentinel + "jobtarget", Priority: jobs.ClassBackfill, DedupeKey: "seed-backfill"})
		if err != nil {
			return err
		}
		require.Equal(t, seedBackfillJob, id)

		if err := tx.SetHeartbeat(ctx, model.Heartbeat{PID: 42, Phase: model.DaemonRunning, CurrentJob: seedOtherJob,
			Interval: 10 * time.Second, SlowFactor: 1.5, DayCount: 3, BackoffUntil: seedTime.Add(time.Minute)}); err != nil {
			return err
		}
		// A failed request: status lists it, with every text field a sentinel.
		return tx.InsertRequest(ctx, store.RequestRow{Time: seedTime, ReqID: sentinel + "req", JobID: seedBackfillJob,
			Kind: "listing", Path: "/tags/" + sentinel + "reqpath/works", Status: 429, Class: "rate_limited",
			Reason: sentinel + "reason", Duration: time.Second})
	}))
}
