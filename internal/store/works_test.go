package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// A failed fetch of an unknown work leaves no placeholder row (it would be an empty 'ok' work),
// and a status change clears an earlier failure so a revived work is not held back by it.
func TestMarkWorkFetchFailed(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()

	require.NoError(t, s.MarkWorkFetchFailed(ctx, 7))
	_, err := s.WorkDiag(ctx, 7)
	require.ErrorIs(t, err, ErrNoWork, "failed fetch of an unknown work created a row")

	_, err = s.UpsertWork(ctx, fixtureWork(8), model.FromBlurb, 1)
	require.NoError(t, err)
	require.NoError(t, s.MarkWorkFetchFailed(ctx, 8))
	d, err := s.WorkDiag(ctx, 8)
	require.NoError(t, err)
	require.False(t, d.FetchFailedAt.IsZero())

	require.NoError(t, s.SetWorkStatus(ctx, 8, model.StatusDeleted))
	d, err = s.WorkDiag(ctx, 8)
	require.NoError(t, err)
	require.True(t, d.FetchFailedAt.IsZero(), "status change kept the fetch failure")

	// Revived by a listing with unchanged stats: still needs its first fetch, not held back.
	res, err := s.UpsertWork(ctx, fixtureWork(8), model.FromBlurb, 1)
	require.NoError(t, err)
	require.True(t, res.Revived)
	require.True(t, res.NeedsFetch, "revived work held back by a stale fetch failure")
}
