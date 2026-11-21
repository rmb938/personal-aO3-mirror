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
	row := model.Source{Kind: "tag", Target: "Fixture Fandom", FiltersJSON: "{}", Path: "/tags/Fixture%20Fandom/works",
		State: model.SourceActive, BackfillOrder: "kudos_desc", PollInterval: 6 * time.Hour, TotalWorks: 3, EstRequests: 4, BackfillCursor: "{}"}
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
			VALUES ('backfill', ?, 1, 4, ?, ?, ?, ?)`, id, st, st+time.Now().String(), now, now)
		require.NoError(t, err)
	}
	require.NoError(t, s.SetSourceState(ctx, id, model.SourcePaused, model.SourceActive))
	err = s.SetSourceState(ctx, id, model.SourceActive, model.SourcePendingConfirm)
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

func TestListSourcesPaged(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	n, err := s.CountSources(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	page, err := s.ListSources(ctx, 0, 10)
	require.NoError(t, err)
	require.Empty(t, page)

	var ids []int64
	for i := range 5 {
		id, err := s.InsertSource(ctx, model.Source{Kind: "tag", Target: "Fixture Tag " + string(rune('A'+i)), FiltersJSON: "{}",
			Path: "/tags/x/works", State: model.SourceActive, BackfillCursor: "{}"})
		require.NoError(t, err)
		ids = append(ids, id)
	}
	// Memberships on the second source, so the counts are computed for paged rows too.
	_, err = s.UpsertWork(ctx, fixtureWork(1), model.FromPage, 1)
	require.NoError(t, err)
	_, err = s.LinkSourceWorks(ctx, ids[1], []int64{1})
	require.NoError(t, err)
	// Deleting one leaves a gap in the ids: paging is by position, not id range.
	require.NoError(t, s.DeleteSource(ctx, ids[2]))
	want := []int64{ids[0], ids[1], ids[3], ids[4]}

	n, err = s.CountSources(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, n)
	var got []int64
	for offset := 0; offset < n; offset += 3 {
		page, err := s.ListSources(ctx, offset, 3)
		require.NoError(t, err)
		for _, r := range page {
			got = append(got, r.ID)
			if r.ID == ids[1] {
				require.Equal(t, 1, r.MemberWorks)
				require.Equal(t, 1, r.FetchedWorks)
			}
		}
	}
	require.Equal(t, want, got, "pages cover every source once, in id order")
	page, err = s.ListSources(ctx, 4, 3)
	require.NoError(t, err)
	require.Empty(t, page, "offset past the end")
	for _, bad := range [][2]int{{-1, 10}, {0, 0}, {0, -1}} {
		_, err = s.ListSources(ctx, bad[0], bad[1])
		require.Error(t, err, "offset %d limit %d", bad[0], bad[1])
	}
}
