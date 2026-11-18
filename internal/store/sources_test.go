package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

func TestSourcesLifecycle(t *testing.T) {
	s, clk, _ := openTest(t)
	ctx := context.Background()
	row := SourceRow{Kind: "tag", Target: "Fixture Fandom", FiltersJSON: "{}", Path: "/tags/Fixture%20Fandom/works",
		State: SourceActive, BackfillOrder: "kudos_desc", PollInterval: 6 * time.Hour, TotalWorks: 3, EstRequests: 4, BackfillCursor: "{}"}
	id, err := s.InsertSource(ctx, row)
	require.NoError(t, err)
	_, err = s.InsertSource(ctx, row)
	require.ErrorIs(t, err, ErrSourceExists)
	found, err := s.FindSource(ctx, "tag", "Fixture Fandom", "{}")
	require.NoError(t, err)
	require.Equal(t, id, found)

	_, err = s.UpsertWork(ctx, fixtureWork(1), model.FromBlurb, 1)
	require.NoError(t, err)
	_, err = s.UpsertWork(ctx, fixtureWork(2), model.FromPage, 1)
	require.NoError(t, err)
	added, err := s.LinkSourceWorks(ctx, id, []int64{1, 2})
	require.NoError(t, err)
	require.Equal(t, 2, added)
	// Two strikes: every missed rescan asks for a check; a successful fetch drops the membership
	// only after two misses in a row, and a sighting resets the count.
	clk.t = clk.t.Add(time.Second)
	_, err = s.LinkSourceWorks(ctx, id, []int64{1}) // work 1 seen by the rescan, work 2 not
	require.NoError(t, err)
	since := clk.t
	check, _, err := s.RecordMissedRescan(ctx, id, since)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, check)
	dropped, err := s.DropMissedSourceWorks(ctx, 2)
	require.NoError(t, err)
	require.Zero(t, dropped, "one miss must not drop the membership")
	_, err = s.LinkSourceWorks(ctx, id, []int64{2}) // seen again: count reset
	require.NoError(t, err)
	clk.t = clk.t.Add(time.Second)
	since = clk.t
	check, _, err = s.RecordMissedRescan(ctx, id, since)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{1, 2}, check)
	dropped, err = s.DropMissedSourceWorks(ctx, 2)
	require.NoError(t, err)
	require.Zero(t, dropped, "a sighting must reset the miss count")
	check, _, err = s.RecordMissedRescan(ctx, id, since)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{1, 2}, check, "second miss in a row still asks for a check")
	require.NoError(t, s.SetWorkStatus(ctx, 1, model.StatusDeleted)) // the check found work 1 deleted
	check, _, err = s.RecordMissedRescan(ctx, id, since)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, check, "deleted works are not counted")
	dropped, err = s.DropMissedSourceWorks(ctx, 2)
	require.NoError(t, err)
	require.EqualValues(t, 1, dropped, "a fetch after two misses in a row drops the membership")
	_, err = s.UpsertWork(ctx, fixtureWork(1), model.FromBlurb, 1) // work 1 revived (another listing)
	require.NoError(t, err)
	var missed int
	require.NoError(t, s.meta.QueryRow(`SELECT missed_rescans FROM source_works WHERE source_id = ? AND work_id = 1`, id).Scan(&missed))
	require.Zero(t, missed, "revival must reset the miss count")
	// A work whose fetch keeps giving up can't be checked: dropped at the second miss, no check.
	require.NoError(t, s.MarkWorkFetchFailed(ctx, 1))
	check, dropped, err = s.RecordMissedRescan(ctx, id, since)
	require.NoError(t, err)
	require.Equal(t, []int64{1}, check, "first miss of an unfetchable work still gets a check")
	require.Zero(t, dropped)
	check, dropped, err = s.RecordMissedRescan(ctx, id, since)
	require.NoError(t, err)
	require.Empty(t, check, "unfetchable work checked again")
	require.EqualValues(t, 1, dropped, "unfetchable work not dropped at the second miss")
	added, err = s.LinkSourceWorks(ctx, id, []int64{1, 2})
	require.NoError(t, err)
	require.Equal(t, 2, added, "both dropped works are new to the source again")
	got, err := s.GetSource(ctx, id)
	require.NoError(t, err)
	require.Equal(t, 2, got.MemberWorks)
	require.Equal(t, 1, got.FetchedWorks)
	require.Equal(t, 6*time.Hour, got.PollInterval)

	// Jobs: queued ones get cancelled, running ones are left alone.
	now := time.Now().Unix()
	for _, st := range []string{"queued", "queued", "running"} {
		_, err := s.meta.Exec(`INSERT INTO jobs (kind, source_id, target, priority_class, state, dedupe_key, created_at, updated_at)
			VALUES ('listing_page', ?, '/x', 4, ?, ?, ?, ?)`, id, st, st+time.Now().String(), now, now)
		require.NoError(t, err)
	}
	require.NoError(t, s.SetSourceState(ctx, id, SourcePaused, SourceActive))
	err = s.SetSourceState(ctx, id, SourceActive, SourcePendingConfirm)
	require.ErrorIs(t, err, ErrStateConflict, "bad transition should fail")
	require.ErrorContains(t, err, "is paused")
	n, err := s.CancelSourceJobs(ctx, id)
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
	require.NoError(t, s.DeleteSource(ctx, id))
	_, err = s.GetSource(ctx, id)
	require.ErrorIs(t, err, ErrNoSource)

	var works, members int
	require.NoError(t, s.meta.QueryRow(`SELECT COUNT(*) FROM works`).Scan(&works))
	require.NoError(t, s.meta.QueryRow(`SELECT COUNT(*) FROM source_works`).Scan(&members))
	require.Equal(t, 2, works, "works must be kept")
	require.Equal(t, 0, members, "memberships removed")
}
