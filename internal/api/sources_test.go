package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
)

// getJSON GETs target, requires status 200 and decodes the body into a T.
func getJSON[T any](t *testing.T, h http.Handler, target string) T {
	t.Helper()
	w := get(h, target)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var v T
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
	return v
}

func isHash(s string) bool { return strings.HasPrefix(s, "h_") && len(s) == 26 }

func TestListSources(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	h := a.Public()

	l := getJSON[dto.List[dto.Source]](t, h, "/api/v1/sources")
	assert.Equal(t, int64(1), l.Page)
	assert.Equal(t, int64(defaultPerPage), l.PerPage)
	assert.Equal(t, 2, l.Total)
	require.Len(t, l.Items, 2)
	tag := l.Items[0]
	assert.Equal(t, seedTagSource, tag.ID)
	assert.Equal(t, dto.SourceKind("tag"), tag.Kind)
	assert.Equal(t, dto.SourceState("active"), tag.State)
	assert.Equal(t, dto.Order("kudos_desc"), tag.Order)
	assert.True(t, isHash(tag.Target), "target hashed")
	assert.True(t, isHash(tag.Path), "path hashed")
	assert.True(t, isHash(tag.LastError), "last_error hashed whole")
	require.Len(t, tag.Filters, 1)
	assert.True(t, isHash(tag.Filters[0].Name))
	assert.True(t, isHash(tag.Filters[0].Values[0]))
	require.NotNil(t, tag.TotalWorks)
	assert.Equal(t, 40, *tag.TotalWorks)
	assert.Nil(t, tag.EstRequests)
	assert.Equal(t, dto.Duration(time.Hour), tag.PollInterval)
	assert.Contains(t, get(h, "/api/v1/sources/1").Body.String(), `"poll_interval":"1h0m0s"`, "a duration string, unhashed")
	assert.Equal(t, seedTime, tag.LastPollAt)
	require.NotNil(t, tag.Backfill)
	assert.Equal(t, dto.Backfill{NextPage: 3, Order: "kudos_desc", WindowStartOn: "2020-01-01", WindowEndOn: "2020-12-31",
		Rescan: true, RescanStartedAt: seedTime, RetryAt: seedTime.Add(time.Minute)}, *tag.Backfill)

	l = getJSON[dto.List[dto.Source]](t, h, "/api/v1/sources?page=2&per_page=1")
	require.Len(t, l.Items, 1)
	assert.Equal(t, seedSeriesSource, l.Items[0].ID)
	assert.Equal(t, 2, l.Total)

	w := get(h, "/api/v1/sources?page=3&per_page=1")
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"items":[],"page":3,"per_page":1,"total":2}`, w.Body.String(), "past the end: items is []")

	for target, field := range map[string]dto.ParamName{
		"/api/v1/sources?per_page=0":             dto.ParamPerPage,
		"/api/v1/sources?per_page=101":           dto.ParamPerPage,
		"/api/v1/sources?page=0":                 dto.ParamPage,
		"/api/v1/sources?page=1002&per_page=100": dto.ParamPage,
	} {
		requireParamProblem(t, get(h, target), field, target)
	}
}

func TestGetSource(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	h := a.Public()

	s := getJSON[dto.Source](t, h, "/api/v1/sources/2")
	assert.Equal(t, seedSeriesSource, s.ID)
	assert.Equal(t, int64(12345), s.TargetID)
	assert.Equal(t, dto.SourceState("pending_confirm"), s.State)
	assert.Nil(t, s.TotalWorks)
	assert.Nil(t, s.Backfill)
	assert.Empty(t, s.Order)
	assert.Equal(t, []dto.Filter{}, s.Filters)

	// Equal strings hash equally across routes within an epoch.
	l := getJSON[dto.List[dto.Source]](t, h, "/api/v1/sources")
	one := getJSON[dto.Source](t, h, "/api/v1/sources/1")
	assert.Equal(t, l.Items[0].Target, one.Target)

	w := get(h, "/api/v1/sources/99")
	require.Equal(t, http.StatusNotFound, w.Code)
	p := decodeProblem(t, w)
	assert.Equal(t, dto.TypeNotFound, p.Type)
	assert.Equal(t, detailNoSource.Message(), p.Detail)
	for _, id := range []string{"0", "abc", "-1", "9007199254740992"} {
		requireParamProblem(t, get(h, "/api/v1/sources/"+id), dto.ParamID, id)
	}
	requireParamProblem(t, get(h, "/api/v1/sources/1?page=1"), dto.ParamPage, "declared name, not taken here")
}

func TestGetSourceAdd(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	h := a.Public()

	done := getJSON[dto.SourceAdd](t, h, "/api/v1/source-adds/1")
	assert.Equal(t, seedDoneAdd, done.JobID)
	assert.Equal(t, dto.JobState("done"), done.State)
	assert.Empty(t, done.LastErrorCode)
	require.NotNil(t, done.Result)
	assert.Equal(t, seedTagSource, done.Result.Source.ID)
	assert.Equal(t, 20, done.Result.Blurbs)
	assert.Equal(t, dto.Estimate{Total: 40, Pages: 2, ListingRequests: 1, WorkFetches: 40, Requests: 41, Duration: dto.Duration(410 * time.Second),
		DiskBytes: 1 << 20}, done.Result.Estimate)

	failed := getJSON[dto.SourceAdd](t, h, "/api/v1/source-adds/2")
	assert.Equal(t, dto.JobState("failed"), failed.State)
	assert.Equal(t, dto.JobErrorCode("not_found"), failed.LastErrorCode, "code passes unhashed")
	assert.True(t, isHash(failed.LastErrorDetail), "detail hashed")
	assert.Nil(t, failed.Result)

	for _, id := range []string{"3", "99"} { // a work job, and no job
		w := get(h, "/api/v1/source-adds/"+id)
		require.Equal(t, http.StatusNotFound, w.Code, id)
		assert.Equal(t, dto.TypeNotFound, decodeProblem(t, w).Type)
	}
	requireParamProblem(t, get(h, "/api/v1/source-adds/x"), dto.ParamJobID, "bad job id")
}

// The access log names the numeric path ID, never the response's content.
func TestSourceAccessLog(t *testing.T) {
	a, logs := newTestAPI(t, nil)
	w := do(a.Public(), httptest.NewRequest(http.MethodGet, "/api/v1/source-adds/2", nil))
	require.Equal(t, http.StatusOK, w.Code)
	l := logs.accessLine(t)
	assert.Equal(t, "/api/v1/source-adds/{job_id}", l["route"])
	assert.InDelta(t, 2, l["path_job_id"], 0)
}
