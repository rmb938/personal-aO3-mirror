package convert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

func TestJob(t *testing.T) {
	loc := time.FixedZone("x", -7200)
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, loc)
	j := model.Job{
		ID: 9, Kind: "backfill", SourceID: 4, Target: "3", Priority: 4, State: model.JobFailed,
		NotBefore: created.Add(time.Minute), Attempts: 2, MaxAttempts: 5, LastError: "not_found:page 3",
		DedupeKey: "backfill:4:3", CreatedAt: created, StartedAt: created.Add(2 * time.Minute),
		FinishedAt: created.Add(3 * time.Minute),
	}
	got := Job(j)
	assert.Equal(t, dto.Job{
		ID: 9, Kind: "backfill", Target: "3", SourceID: 4, Priority: 4, State: "failed", Attempts: 2,
		MaxAttempts: 5, LastErrorCode: "not_found", LastErrorDetail: "page 3",
		NotBeforeAt: created.Add(time.Minute).UTC(), CreatedAt: created.UTC(),
		StartedAt: created.Add(2 * time.Minute).UTC(), FinishedAt: created.Add(3 * time.Minute).UTC(),
	}, got)
	assert.Equal(t, time.UTC, got.CreatedAt.Location())

	got = Job(model.Job{ID: 1, Kind: "work", Target: "7", State: model.JobQueued})
	assert.Zero(t, got.SourceID)
	assert.Zero(t, got.LastErrorCode)
	assert.True(t, got.NotBeforeAt.IsZero() && got.StartedAt.IsZero() && got.FinishedAt.IsZero(), "unset times stay zero")
}

func TestJobList(t *testing.T) {
	got := JobList(nil, 0, 1, 20)
	assert.Equal(t, dto.List[dto.Job]{Page: 1, PerPage: 20}, got, "empty page: nil items")
	got = JobList([]model.Job{{ID: 2}, {ID: 1}}, 5, 1, 2)
	assert.Equal(t, []int64{2, 1}, []int64{got.Items[0].ID, got.Items[1].ID})
	assert.Equal(t, 5, got.Total)
}
