package store

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// jobPlanFilters are the API's GET /jobs filter combinations (state, kind, source_id).
var jobPlanFilters = []model.JobFilter{
	{},
	{State: "done"},
	{Kind: "work"},
	{SourceID: 1},
	{State: "done", Kind: "work"},
	{State: "done", SourceID: 1},
	{Kind: "work", SourceID: 1},
	{State: "done", Kind: "work", SourceID: 1},
}

// explain returns q's EXPLAIN QUERY PLAN details joined by "; ".
func explain(t *testing.T, s *testStore, q string, args ...any) string {
	t.Helper()
	rows, err := s.meta.QueryContext(context.Background(), `EXPLAIN QUERY PLAN `+q, args...)
	require.NoError(t, err)
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &notused, &detail))
		lines = append(lines, detail)
	}
	require.NoError(t, rows.Err())
	return strings.Join(lines, "; ")
}

// A jobs page (newest first) must not sort every matching row. A source's jobs are the
// accepted exception: one source holds a small share of the table (see BenchmarkJobsPage).
func TestJobsPagePlans(t *testing.T) {
	s, _, _ := openTest(t)
	for _, f := range jobPlanFilters {
		where, args := jobWhere(f)
		plan := explain(t, s, `SELECT `+jobCols+` FROM jobs`+where+` ORDER BY id DESC LIMIT 1 OFFSET 1`, args...)
		if f.SourceID > 0 && f.State == "" {
			continue
		}
		require.NotContains(t, plan, "TEMP B-TREE", "%s: %s", filterName(f), plan)
	}
}
