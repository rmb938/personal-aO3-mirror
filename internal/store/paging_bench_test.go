package store

// Benchmarks for paging GET /api/v1/jobs and /sources over large synthetic tables. No real data.
//
//	go test ./internal/store -run '^$' -bench 'Page|Count' -benchtime 20x
//
// Size: done and cancelled jobs are pruned after 7 days (at most ~60k fetches at the 10s
// default interval), failed ones after 30; a large backfill can queue ~100k work fetches.

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

const (
	benchJobs    = 200_000
	benchSources = 20
)

// seedJobs inserts benchJobs synthetic jobs: ~60% queued, ~35% done, the rest failed or
// cancelled; ~90% work fetches, the rest listing pages and info jobs; source ids spread over
// benchSources sources (info jobs have none).
func seedJobs(b *testing.B) *testStore {
	b.Helper()
	s, _, _ := openTest(b)
	for i := range benchSources {
		addSource(b, s, "Fixture Tag "+strconv.Itoa(i), model.SourceActive, 0)
	}
	// Kind, source and state come from different digits of i so they are not correlated.
	// Dedupe keys are unique so the active-job unique index never conflicts.
	_, err := s.meta.ExecContext(context.Background(), `
		WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i + 1 < ?)
		INSERT INTO jobs (kind, source_id, target, priority_class, state, dedupe_key,
			created_at, updated_at, started_at, finished_at)
		SELECT
			CASE i % 20 WHEN 0 THEN 'poll' WHEN 1 THEN 'backfill' WHEN 2 THEN 'tag_info' ELSE 'work' END,
			CASE i % 20 WHEN 2 THEN NULL ELSE (i / 20) % ? + 1 END,
			i, 2,
			CASE WHEN (i / 7) % 100 < 60 THEN 'queued' WHEN (i / 7) % 100 < 97 THEN 'done'
				WHEN (i / 7) % 100 < 99 THEN 'failed' ELSE 'cancelled' END,
			'bench:' || i, i, i, i, i
		FROM n`, benchJobs, benchSources)
	require.NoError(b, err)
	return s
}

func BenchmarkJobsPage(b *testing.B) {
	s := seedJobs(b)
	ctx := context.Background()
	for _, f := range jobPlanFilters {
		for _, offset := range []int{0, 100_000 - 100} {
			f := f
			f.Offset, f.Limit = offset, 100
			b.Run(fmt.Sprintf("%s/offset=%d", filterName(f), offset), func(b *testing.B) {
				for b.Loop() {
					_, err := s.FindJobs(ctx, f)
					require.NoError(b, err)
				}
			})
		}
	}
}

func BenchmarkJobsCount(b *testing.B) {
	s := seedJobs(b)
	ctx := context.Background()
	for _, f := range jobPlanFilters {
		b.Run(filterName(f), func(b *testing.B) {
			for b.Loop() {
				_, err := s.CountJobs(ctx, f)
				require.NoError(b, err)
			}
		})
	}
}

// filterName names a GET /jobs filter combination for a benchmark.
func filterName(f model.JobFilter) string {
	n := ""
	if f.State != "" {
		n += "state,"
	}
	if f.Kind != "" {
		n += "kind,"
	}
	if f.SourceID > 0 {
		n += "source,"
	}
	if n == "" {
		return "none"
	}
	return n[:len(n)-1]
}

// BenchmarkSourcesPage checks that rows skipped by OFFSET do not run ListSources' member-count
// subqueries: a deep page should cost about the same as the first one.
func BenchmarkSourcesPage(b *testing.B) {
	const sources, works = 1000, 200
	s, _, _ := openTest(b)
	ctx := context.Background()
	for i := range sources {
		addSource(b, s, "Fixture Tag "+strconv.Itoa(i), model.SourceActive, 0)
	}
	for id := range int64(works) {
		_, err := s.UpsertWork(ctx, fixtureWork(id+1), model.FromPage, 1)
		require.NoError(b, err)
	}
	_, err := s.meta.ExecContext(ctx, `INSERT INTO source_works (source_id, work_id, first_seen, last_seen)
		SELECT s.id, w.id, 0, 0 FROM sources s, works w`)
	require.NoError(b, err)
	for _, offset := range []int{0, sources - 100} {
		b.Run(fmt.Sprintf("offset=%d", offset), func(b *testing.B) {
			for b.Loop() {
				page, err := s.ListSources(ctx, offset, 100)
				require.NoError(b, err)
				require.Len(b, page, 100)
			}
		})
	}
}
