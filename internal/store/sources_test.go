package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

func TestSourcesLifecycle(t *testing.T) {
	s, _, _ := openTest(t)
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
	require.NoError(t, s.LinkSourceWorks(ctx, id, []int64{1, 2}))
	got, err := s.GetSource(ctx, id)
	require.NoError(t, err)
	require.Equal(t, 2, got.MemberWorks)
	require.Equal(t, 1, got.FetchedWorks)
	require.Equal(t, 6*time.Hour, got.PollInterval)

	// Jobs: queued ones get cancelled, running ones are left alone.
	now := time.Now().Unix()
	for _, st := range []string{"queued", "queued", "running"} {
		_, err := s.Meta.Exec(`INSERT INTO jobs (kind, source_id, target, priority_class, state, dedupe_key, created_at, updated_at)
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
	require.NoError(t, s.Meta.QueryRow(`SELECT COUNT(*) FROM works`).Scan(&works))
	require.NoError(t, s.Meta.QueryRow(`SELECT COUNT(*) FROM source_works`).Scan(&members))
	require.Equal(t, 2, works, "works must be kept")
	require.Equal(t, 0, members, "memberships removed")
}
