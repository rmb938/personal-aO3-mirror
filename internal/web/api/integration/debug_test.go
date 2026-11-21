package integration

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

func TestDebugJob(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Admin()

	d := getJSON[dto.JobDebug](t, h, "/api/v1/debug/jobs/4")
	assert.Equal(t, seedBackfillJob, d.Job.ID)
	assert.Equal(t, dto.JobKind("backfill"), d.Job.Kind)
	assert.True(t, isHash(t, d.DedupeKey), "dedupe key hashed")
	require.Len(t, d.Events, 2)
	start := d.Events[0]
	assert.Equal(t, dto.JobEventType("start"), start.Event)
	assert.Equal(t, seedTime, start.OccurredAt)
	assert.Equal(t, new(1), start.Outcome.Attempt)
	assert.Equal(t, new(int64(3)), start.Outcome.Target, "target is a plain number")
	assert.Equal(t, &dto.BackfillDetail{}, start.Backfill, "a start event has the kind's member, all null")

	retry := d.Events[1]
	assert.Equal(t, dto.JobEventType("retry"), retry.Event)
	o := retry.Outcome
	assert.Equal(t, new(dto.Duration(1500*time.Millisecond)), o.Duration)
	assert.Equal(t, new(dto.JobErrorCode("parse_error")), o.ErrorCode)
	assert.True(t, isHash(t, *o.ErrorDetail), "error detail hashed")
	assert.Equal(t, new(dto.JobState("queued")), o.NextState)
	assert.Equal(t, new(seedTime.Add(time.Minute)), o.NotBeforeAt)
	bf := retry.Backfill
	require.NotNil(t, bf)
	assert.Equal(t, new(2), bf.Page)
	assert.Equal(t, new(0), bf.New, "a zero is kept")
	assert.Nil(t, bf.NextPage, "unset")
	assert.True(t, isHash(t, *bf.ReqID), "req_id hashed")
	assert.Equal(t, new("2020-01-01"), bf.WindowStartOn, "dates pass")
	assert.Nil(t, retry.Work, "other kinds omitted")

	// On the wire every detail field is present, null when unset.
	var raw struct {
		Events []map[string]json.RawMessage `json:"events"`
	}
	w := testutil.Get(t, h, "/api/v1/debug/jobs/4")
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw.Events[1]["backfill"], &fields))
	assert.Equal(t, "null", string(fields["next_page"]))
	assert.Equal(t, "0", string(fields["new"]))
	assert.Len(t, fields, reflect.TypeFor[dto.BackfillDetail]().NumField(), "every field")
	assert.NotContains(t, raw.Events[1], "work", "other kinds omitted")

	require.Len(t, d.Requests, 1, "only this job's requests")
	assert.Equal(t, dto.RequestClass("rate_limited"), d.Requests[0].Class)

	// A source add's done event, as the add handler writes it.
	d = getJSON[dto.JobDebug](t, h, "/api/v1/debug/jobs/1")
	require.Len(t, d.Events, 1)
	sa := d.Events[0].SourceAdd
	require.NotNil(t, sa)
	assert.Equal(t, new(seedTagSource), sa.SourceID)
	assert.Equal(t, new(20), sa.Blurbs)
	require.NotNil(t, sa.Estimate)
	assert.Equal(t, dto.Duration(410*time.Second), sa.Estimate.Duration)
	assert.Nil(t, sa.State, "not in this event")

	d = getJSON[dto.JobDebug](t, h, "/api/v1/debug/jobs/3")
	assert.Equal(t, []dto.JobEvent{}, d.Events, "no events: [], not null")

	w = testutil.Get(t, h, "/api/v1/debug/jobs/99")
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, runtime.DetailNoJob.Message(), testutil.DecodeProblem(t, w).Detail)
	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/debug/jobs/0"), dto.ParamID, "id 0")
	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/debug/jobs/4?verbose=1"), "", "unknown param")
}

func TestDebugWork(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Admin()

	d := getJSON[dto.WorkDebug](t, h, "/api/v1/debug/works/7")
	wd := d.Work
	assert.Equal(t, seedWork, wd.ID)
	assert.Equal(t, dto.WorkStatus("ok"), wd.Status)
	assert.Equal(t, "2025-03-04", wd.RevisedOn, "dates pass the enforcer")
	assert.Empty(t, wd.PublishedOn, "a blurb has no published date")
	assert.Equal(t, 1200, wd.Words)
	assert.Equal(t, 5, wd.Stats.Kudos)
	assert.Equal(t, seedTime, wd.FirstSeenAt)
	assert.Equal(t, dto.WorkRows{Tags: 1, Creators: 1}, wd.Rows)
	require.Len(t, d.Jobs, 1)
	assert.Equal(t, seedOtherJob, d.Jobs[0].ID)
	require.Len(t, d.Requests, 1, "only the work's requests")
	assert.Equal(t, dto.RequestKind("work"), d.Requests[0].Kind)
	assert.True(t, isHash(t, d.Requests[0].Path) && isHash(t, d.Requests[0].Reason), "request text hashed")

	w := testutil.Get(t, h, "/api/v1/debug/works/8")
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, runtime.DetailNoWork.Message(), testutil.DecodeProblem(t, w).Detail)
	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/debug/works/x"), dto.ParamID, "bad id")
}

// The debug routes do not exist on the public listener.
func TestDebugNotPublic(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	for _, target := range []string{"/api/v1/debug/jobs/4", "/api/v1/debug/works/7"} {
		w := testutil.Get(t, a.Public(), target)
		require.Equal(t, http.StatusNotFound, w.Code, target)
		assert.Equal(t, dto.TypeNotFound, testutil.DecodeProblem(t, w).Type, target)
	}
}
