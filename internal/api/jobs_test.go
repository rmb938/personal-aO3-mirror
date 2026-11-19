package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
)

func jobIDs(l dto.List[dto.Job]) []int64 {
	ids := make([]int64, len(l.Items))
	for i, j := range l.Items {
		ids[i] = j.ID
	}
	return ids
}

func TestListJobs(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	h := a.Public()

	l := getJSON[dto.List[dto.Job]](t, h, "/api/v1/jobs")
	assert.Equal(t, 4, l.Total)
	assert.Equal(t, int64(defaultPerPage), l.PerPage)
	assert.Equal(t, []int64{seedBackfillJob, seedOtherJob, seedFailedAdd, seedDoneAdd}, jobIDs(l), "newest first")

	failed := l.Items[2]
	assert.Equal(t, dto.JobKind("source_add"), failed.Kind)
	assert.Equal(t, dto.JobState("failed"), failed.State)
	assert.Equal(t, dto.JobErrorCode("not_found"), failed.LastErrorCode)
	assert.True(t, isHash(failed.LastErrorDetail), "last_error_detail hashed")
	assert.True(t, isHash(failed.Target), "target hashed")
	assert.Equal(t, seedTime, failed.FinishedAt)

	for target, want := range map[string][]int64{
		"/api/v1/jobs?state=queued":                   {seedOtherJob, seedBackfillJob}, // queue order
		"/api/v1/jobs?state=done":                     {seedDoneAdd},
		"/api/v1/jobs?kind=source_add":                {seedFailedAdd, seedDoneAdd},
		"/api/v1/jobs?kind=work&state=queued":         {seedOtherJob},
		"/api/v1/jobs?source_id=1":                    {seedBackfillJob},
		"/api/v1/jobs?source_id=2":                    {},
		"/api/v1/jobs?state=queued&page=2&per_page=1": {seedBackfillJob},
	} {
		l := getJSON[dto.List[dto.Job]](t, h, target)
		assert.Equal(t, want, jobIDs(l), target)
	}
	l = getJSON[dto.List[dto.Job]](t, h, "/api/v1/jobs?state=queued&page=2&per_page=1")
	assert.Equal(t, 2, l.Total, "total counts every page")
	bf := l.Items[0]
	assert.Equal(t, seedTagSource, bf.SourceID)
	assert.Equal(t, dto.JobKind("backfill"), bf.Kind)
	assert.Empty(t, bf.LastErrorCode)

	for target, field := range map[string]dto.ParamName{
		"/api/v1/jobs?state=bogus":  dto.ParamState,
		"/api/v1/jobs?state=":       dto.ParamState,
		"/api/v1/jobs?state=QUEUED": dto.ParamState,
		"/api/v1/jobs?kind=tag":     dto.ParamKind,
		"/api/v1/jobs?source_id=0":  dto.ParamSourceID,
		"/api/v1/jobs?source_id=x":  dto.ParamSourceID,
		"/api/v1/jobs?per_page=101": dto.ParamPerPage,
		"/api/v1/jobs?page=0":       dto.ParamPage,
	} {
		w := get(h, target)
		require.Equal(t, http.StatusBadRequest, w.Code, target)
		p := decodeProblem(t, w)
		assert.Equal(t, dto.CodeInvalidParam, p.Code, target)
		assert.Equal(t, field, p.Field, target)
	}
	w := get(h, "/api/v1/jobs?target=1")
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, dto.CodeUnknownParam, decodeProblem(t, w).Code)
}

func TestGetJob(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	h := a.Public()

	j := getJSON[dto.Job](t, h, "/api/v1/jobs/3")
	assert.Equal(t, seedOtherJob, j.ID)
	assert.Equal(t, dto.JobKind("work"), j.Kind)
	assert.Equal(t, dto.JobState("queued"), j.State)
	assert.Zero(t, j.SourceID)
	assert.Equal(t, seedTime, j.CreatedAt)
	assert.True(t, j.StartedAt.IsZero())

	// Equal strings hash equally across routes within an epoch.
	l := getJSON[dto.List[dto.Job]](t, h, "/api/v1/jobs?kind=work")
	assert.Equal(t, l.Items[0].Target, j.Target)

	w := get(h, "/api/v1/jobs/99")
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, dto.CodeNotFound, decodeProblem(t, w).Code)
	for _, id := range []string{"0", "abc", "-1", "9007199254740992"} {
		w := get(h, "/api/v1/jobs/"+id)
		require.Equal(t, http.StatusBadRequest, w.Code, id)
		p := decodeProblem(t, w)
		assert.Equal(t, dto.CodeInvalidParam, p.Code)
		assert.Equal(t, dto.ParamID, p.Field)
	}
	w = get(h, "/api/v1/jobs/1?state=done")
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, dto.CodeUnknownParam, decodeProblem(t, w).Code)
}
