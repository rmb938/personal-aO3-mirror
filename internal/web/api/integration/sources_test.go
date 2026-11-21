package integration

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

// getJSON GETs target, requires status 200 and decodes the body into a T.
func getJSON[T any](t *testing.T, h http.Handler, target string) T {
	t.Helper()
	w := testutil.Get(t, h, target)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var v T
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
	return v
}

func isHash(t *testing.T, s string) bool {
	t.Helper()
	return strings.HasPrefix(s, "h_") && len(s) == 26
}

func TestListSources(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()

	l := getJSON[dto.List[dto.Source]](t, h, "/api/v1/sources")
	assert.Equal(t, int64(1), l.Page)
	assert.Equal(t, int64(runtime.DefaultPerPage), l.PerPage)
	assert.Equal(t, 2, l.Total)
	require.Len(t, l.Items, 2)
	tag := l.Items[0]
	assert.Equal(t, seedTagSource, tag.ID)
	assert.Equal(t, dto.SourceKind("tag"), tag.Kind)
	assert.Equal(t, dto.SourceState("active"), tag.State)
	assert.Equal(t, dto.Order("kudos_desc"), tag.Order)
	assert.True(t, isHash(t, tag.Target), "target hashed")
	assert.True(t, isHash(t, tag.Path), "path hashed")
	assert.True(t, isHash(t, tag.LastError), "last_error hashed whole")
	require.Len(t, tag.Filters, 1)
	assert.True(t, isHash(t, tag.Filters[0].Name))
	assert.True(t, isHash(t, tag.Filters[0].Values[0]))
	require.NotNil(t, tag.TotalWorks)
	assert.Equal(t, 40, *tag.TotalWorks)
	assert.Nil(t, tag.EstRequests)
	assert.Equal(t, dto.Duration(time.Hour), tag.PollInterval)
	assert.Contains(t, testutil.Get(t, h, "/api/v1/sources/1").Body.String(), `"poll_interval":"1h0m0s"`, "a duration string, unhashed")
	assert.Equal(t, seedTime, tag.LastPollAt)
	require.NotNil(t, tag.Backfill)
	assert.Equal(t, dto.Backfill{NextPage: 3, Order: "kudos_desc", WindowStartOn: "2020-01-01", WindowEndOn: "2020-12-31",
		Rescan: true, RescanStartedAt: seedTime, RetryAt: seedTime.Add(time.Minute)}, *tag.Backfill)

	l = getJSON[dto.List[dto.Source]](t, h, "/api/v1/sources?page=2&per_page=1")
	require.Len(t, l.Items, 1)
	assert.Equal(t, seedSeriesSource, l.Items[0].ID)
	assert.Equal(t, 2, l.Total)

	w := testutil.Get(t, h, "/api/v1/sources?page=3&per_page=1")
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"items":[],"page":3,"per_page":1,"total":2}`, w.Body.String(), "past the end: items is []")

	for target, field := range map[string]dto.ParamName{
		"/api/v1/sources?per_page=0":             dto.ParamPerPage,
		"/api/v1/sources?per_page=101":           dto.ParamPerPage,
		"/api/v1/sources?page=0":                 dto.ParamPage,
		"/api/v1/sources?page=1002&per_page=100": dto.ParamPage,
	} {
		testutil.RequireParamProblem(t, testutil.Get(t, h, target), field, target)
	}
}

func TestGetSource(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
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

	w := testutil.Get(t, h, "/api/v1/sources/99")
	require.Equal(t, http.StatusNotFound, w.Code)
	p := testutil.DecodeProblem(t, w)
	assert.Equal(t, dto.TypeNotFound, p.Type)
	assert.Equal(t, runtime.DetailNoSource.Message(), p.Detail)
	for _, id := range []string{"0", "abc", "-1", "9007199254740992"} {
		testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/sources/"+id), dto.ParamID, id)
	}
	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/sources/1?page=1"), dto.ParamPage, "declared name, not taken here")
}

func TestGetSourceAdd(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
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
	assert.True(t, isHash(t, failed.LastErrorDetail), "detail hashed")
	assert.Nil(t, failed.Result)

	for _, id := range []string{"3", "99"} { // a work job, and no job
		w := testutil.Get(t, h, "/api/v1/source-adds/"+id)
		require.Equal(t, http.StatusNotFound, w.Code, id)
		assert.Equal(t, dto.TypeNotFound, testutil.DecodeProblem(t, w).Type)
	}
	testutil.RequireParamProblem(t, testutil.Get(t, h, "/api/v1/source-adds/x"), dto.ParamJobID, "bad job id")
}

// postJSON POSTs body as application/json to target.
func postJSON(t *testing.T, h http.Handler, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", runtime.ContentTypeJSON)
	return testutil.Do(t, h, req)
}

func TestAddSource(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	const tagURL = `{"url":"https://archiveofourown.org/tags/Fixture%20Tag/works","order":"hits_desc","priority":5,"poll_interval":"1h"}`

	w := postJSON(t, h, "/api/v1/sources", tagURL)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	assert.Equal(t, "/api/v1/source-adds/5", w.Header().Get("Location"))
	assert.JSONEq(t, `{"job_id":5,"created":true}`, w.Body.String())
	add := getJSON[dto.SourceAdd](t, h, "/api/v1/source-adds/5")
	assert.Equal(t, dto.JobState("queued"), add.State)

	w = postJSON(t, h, "/api/v1/sources", `{"url":"/tags/Fixture%20Tag/works"}`)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	assert.JSONEq(t, `{"job_id":5,"created":false}`, w.Body.String(), "already queued: the same job")

	// A single work is added at once.
	w = postJSON(t, h, "/api/v1/sources", `{"url":"https://archiveofourown.org/works/777"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Equal(t, "/api/v1/sources/3", w.Header().Get("Location"))
	var res dto.SourceAddResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, int64(3), res.Source.ID)
	assert.Equal(t, dto.SourceKind("work"), res.Source.Kind)
	assert.Equal(t, int64(777), res.Source.TargetID)
	assert.True(t, isHash(t, res.Source.Target), "target hashed")
	assert.Equal(t, 1, res.Estimate.WorkFetches)

	// An existing source: 409 with the source's Location.
	w = postJSON(t, h, "/api/v1/sources", `{"url":"https://archiveofourown.org/series/12345"}`)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	p := testutil.DecodeProblem(t, w)
	assert.Equal(t, dto.TypeAlreadyExists, p.Type)
	assert.Equal(t, runtime.DetailSourceExists.Message(), p.Detail)
	assert.Equal(t, "/api/v1/sources/2", w.Header().Get("Location"))

	// A rejected URL is a validation error at /url with the reason's own hint.
	for u, reason := range map[string]ao3url.Reason{
		"https://example.com/tags/x/works": ao3url.ReasonNotAO3,
		"/works/search?q=x":                ao3url.ReasonWorkSearch,
		"/chapters/5":                      ao3url.ReasonChapter,
		"not a url":                        ao3url.ReasonNotAO3,
		"/tags/x/bookmarks":                ao3url.ReasonTagPage,
	} {
		w := postJSON(t, h, "/api/v1/sources", `{"url":"`+u+`"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code, u)
		p := testutil.DecodeProblem(t, w)
		assert.Equal(t, dto.TypeValidation, p.Type, u)
		assert.Equal(t, []dto.ProblemError{{Detail: reason.Hint(), Pointer: "/url"}}, p.Errors, u)
	}
}

// A rejected URL's problem and logs never repeat the URL.
func TestAddSourceRejectedURLIsContentFree(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	for _, u := range []string{"/tags/" + sentinel + "%zz/works", "https://" + sentinel + ".example/x", "/tags/" + sentinel + "/bookmarks"} {
		w := postJSON(t, a.Public(), "/api/v1/sources", `{"url":"`+u+`"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code, u)
		testutil.AssertNoSentinel(t, w, logs, sentinel)
	}
}

// memberErrors POSTs body and returns the validation error's items as pointer → detail.
func memberErrors(t *testing.T, h http.Handler, body string) map[string]string {
	t.Helper()
	return problemPointers(t, postJSON(t, h, "/api/v1/sources", body), body)
}

// testDetailOrder is request's detail for a bad order (the same catalogue message).
var testDetailOrder = problem.OneOf(dto.Order(""))

// The body's own rules are tested in request; this checks the handler wiring: every invalid
// member (and the listener's confirm rule) comes back at once, by pointer, unhashed.
func TestAddSourceValidation(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	got := memberErrors(t, h, `{"url":"/works/search","order":"x","priority":1001,"poll_interval":"1s","confirm":true}`)
	assert.Equal(t, []string{"/confirm", "/order", "/poll_interval", "/priority", "/url"}, slices.Sorted(maps.Keys(got)))
	assert.Equal(t, ao3url.ReasonWorkSearch.Hint(), got["/url"])
	assert.Equal(t, runtime.DetailConfirmAdminOnly.Message(), got["/confirm"])
	for ptr, d := range got {
		assert.False(t, isHash(t, d), "%s: a catalogue detail passes unhashed", ptr)
	}

	for _, body := range []string{`{"url":"/tags/a/works","extra":1}`, `{"url":"/tags/a/works"}{}`, `{"url":1}`,
		`{"url":"/tags/a/works","poll_interval":3600}`, `{"url":"/tags/a/works","poll_interval":"soon"}`} {
		w := postJSON(t, h, "/api/v1/sources", body)
		require.Equal(t, http.StatusBadRequest, w.Code, body)
		assert.Equal(t, dto.TypeInvalidBody, testutil.DecodeProblem(t, w).Type, body)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sources", strings.NewReader(`{"url":"/tags/a/works"}`))
	w := testutil.Do(t, h, req)
	require.Equal(t, http.StatusUnsupportedMediaType, w.Code, "no content type")

	// Query and body errors are listed together.
	w = postJSON(t, h, "/api/v1/sources?page=1", `{"url":"/tags/a/works","order":"x"}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, []dto.ProblemError{
		{Detail: runtime.DetailUnknownParam.Message(), Parameter: dto.ParamPage},
		{Detail: testDetailOrder.Message(), Pointer: "/order"},
	}, testutil.DecodeProblem(t, w).Errors)

	// The admin socket accepts confirm.
	w = postJSON(t, a.Admin(), "/api/v1/sources", `{"url":"/tags/a/works","confirm":true}`)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
}

func TestAddSourceRateLimit(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, func(o *runtime.Options) { o.SourceAddPerHour = 1 })
	w := postJSON(t, a.Public(), "/api/v1/sources", `{"url":"/tags/a/works"}`)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	w = postJSON(t, a.Public(), "/api/v1/sources", `{"url":"/tags/b/works"}`)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, dto.TypeRateLimited, testutil.DecodeProblem(t, w).Type)
	assert.NotEmpty(t, w.Header().Get("Retry-After"))
	assert.Equal(t, http.StatusOK, testutil.Get(t, a.Public(), "/api/v1/sources").Code, "other routes keep the general limit")

	// The admin socket has a separate bucket with a 10x limit.
	for i := range 10 {
		w := postJSON(t, a.Admin(), "/api/v1/sources", `{"url":"/tags/c`+strconv.Itoa(i)+`/works"}`)
		require.Equal(t, http.StatusAccepted, w.Code, i)
	}
	w = postJSON(t, a.Admin(), "/api/v1/sources", `{"url":"/tags/d/works"}`)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
}

// post sends a bodyless POST to target.
func post(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.Do(t, h, httptest.NewRequest(http.MethodPost, target, nil))
}

// requireNoContent asserts w is an empty 204 that still went through the enforcer.
func requireNoContent(t *testing.T, w *httptest.ResponseRecorder, msg string) {
	t.Helper()
	require.Equal(t, http.StatusNoContent, w.Code, msg+": "+w.Body.String())
	assert.Empty(t, w.Body.String(), msg)
	assert.Empty(t, w.Header().Get("Content-Length"), msg)
	assert.Empty(t, w.Header().Get("Content-Type"), msg)
	assert.Equal(t, "hashed", w.Header().Get(runtime.HeaderFanwork), msg)
}

func sourceState(t *testing.T, h http.Handler, id int64) dto.SourceState {
	t.Helper()
	return getJSON[dto.Source](t, h, "/api/v1/sources/"+strconv.FormatInt(id, 10)).State
}

func TestSourceAdminActions(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Admin()

	requireNoContent(t, post(t, h, "/api/v1/sources/2/confirm"), "confirm")
	assert.Equal(t, dto.SourceState("active"), sourceState(t, h, seedSeriesSource))
	requireNoContent(t, post(t, h, "/api/v1/sources/1/pause"), "pause")
	assert.Equal(t, dto.SourceState("paused"), sourceState(t, h, seedTagSource))
	requireNoContent(t, post(t, h, "/api/v1/sources/1/resume"), "resume")
	assert.Equal(t, dto.SourceState("active"), sourceState(t, h, seedTagSource))

	// A change the source's state does not allow is a conflict.
	for _, target := range []string{"/api/v1/sources/2/confirm", "/api/v1/sources/1/resume"} {
		w := post(t, h, target)
		require.Equal(t, http.StatusConflict, w.Code, target)
		p := testutil.DecodeProblem(t, w)
		assert.Equal(t, dto.TypeStateConflict, p.Type, target)
		assert.Equal(t, runtime.DetailStateConflict.Message(), p.Detail, target)
	}

	// Cancel drops the source's queued backfill job.
	w := post(t, h, "/api/v1/sources/1/cancel")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{"dropped_jobs":1}`, w.Body.String())
	assert.Equal(t, dto.SourceState("cancelled"), sourceState(t, h, seedTagSource))
	assert.Equal(t, dto.JobState("cancelled"), getJSON[dto.Job](t, h, "/api/v1/jobs/4").State)
	assert.Equal(t, http.StatusConflict, post(t, h, "/api/v1/sources/1/cancel").Code, "already cancelled")
	assert.Equal(t, http.StatusConflict, post(t, h, "/api/v1/sources/1/pause").Code, "cancelled is not pausable")

	requireNoContent(t, testutil.Do(t, h, httptest.NewRequest(http.MethodDelete, "/api/v1/sources/1", nil)), "delete")
	assert.Equal(t, http.StatusNotFound, testutil.Get(t, h, "/api/v1/sources/1").Code)

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/v1/sources/99/pause", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/sources/99/cancel", nil),
		httptest.NewRequest(http.MethodDelete, "/api/v1/sources/1", nil),
	} {
		w := testutil.Do(t, h, req)
		require.Equal(t, http.StatusNotFound, w.Code, req.URL.Path)
		assert.Equal(t, runtime.DetailNoSource.Message(), testutil.DecodeProblem(t, w).Detail, req.URL.Path)
	}
	testutil.RequireParamProblem(t, post(t, h, "/api/v1/sources/x/pause"), dto.ParamID, "bad id")
	testutil.RequireParamProblem(t, post(t, h, "/api/v1/sources/2/pause?page=1"), dto.ParamPage, "no query params")
	assert.Equal(t, dto.SourceState("active"), sourceState(t, h, seedSeriesSource), "an invalid request changes nothing")
}

// sendJSON sends body to target as application/json with method.
func sendJSON(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", runtime.ContentTypeJSON)
	return testutil.Do(t, h, req)
}

// problemPointers requires a 422 validation-error and returns its items as pointer → detail.
func problemPointers(t *testing.T, w *httptest.ResponseRecorder, msg string) map[string]string {
	t.Helper()
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, msg+": "+w.Body.String())
	p := testutil.DecodeProblem(t, w)
	assert.Equal(t, dto.TypeValidation, p.Type, msg)
	out := map[string]string{}
	for _, e := range p.Errors {
		out[e.Pointer] = e.Detail
	}
	return out
}

func TestRefreshSource(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Admin()

	w := postJSON(t, h, "/api/v1/sources/1/refresh", `{}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{"poll_now":true,"requeued_works":0,"rescan_started":false,"unblocked_parser":false}`, w.Body.String())
	assert.Equal(t, seedTime, getJSON[dto.Source](t, h, "/api/v1/sources/1").NextPollAt, "poll due now")

	// full clears the backfill's retry wait (the seeded cursor has one).
	require.NotZero(t, getJSON[dto.Source](t, h, "/api/v1/sources/1").Backfill.RetryAt)
	w = postJSON(t, h, "/api/v1/sources/1/refresh", `{"full":true}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Zero(t, getJSON[dto.Source](t, h, "/api/v1/sources/1").Backfill.RetryAt)

	// A pending_confirm source can't be refreshed.
	w = postJSON(t, h, "/api/v1/sources/2/refresh", `{}`)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	p := testutil.DecodeProblem(t, w)
	assert.Equal(t, dto.TypeStateConflict, p.Type)
	assert.Equal(t, runtime.DetailStateConflict.Message(), p.Detail)

	w = postJSON(t, h, "/api/v1/sources/99/refresh", `{}`)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, runtime.DetailNoSource.Message(), testutil.DecodeProblem(t, w).Detail)

	testutil.RequireParamProblem(t, postJSON(t, h, "/api/v1/sources/x/refresh", `{}`), dto.ParamID, "bad id")
	for _, body := range []string{``, `{"full":1}`, `{"full":true,"extra":1}`} {
		w := postJSON(t, h, "/api/v1/sources/1/refresh", body)
		require.Equal(t, http.StatusBadRequest, w.Code, body)
		assert.Equal(t, dto.TypeInvalidBody, testutil.DecodeProblem(t, w).Type, body)
	}
	assert.Equal(t, http.StatusUnsupportedMediaType, post(t, h, "/api/v1/sources/1/refresh").Code, "a body is required")
}

func TestSetSourceSettings(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Admin()
	patch := func(target, body string) *httptest.ResponseRecorder {
		return sendJSON(t, h, http.MethodPatch, target, body)
	}
	settings := func() (time.Duration, int) {
		s := getJSON[dto.Source](t, h, "/api/v1/sources/1")
		return s.PollInterval.Duration(), s.Priority
	}

	requireNoContent(t, patch("/api/v1/sources/1", `{"poll_interval":"2h","priority":-5}`), "both")
	poll, prio := settings()
	assert.Equal(t, 2*time.Hour, poll)
	assert.Equal(t, -5, prio)

	// Absent (or null) members keep their value.
	requireNoContent(t, patch("/api/v1/sources/1", `{"priority":7}`), "priority only")
	requireNoContent(t, patch("/api/v1/sources/1", `{"poll_interval":"90m","priority":null}`), "poll only")
	requireNoContent(t, patch("/api/v1/sources/1", `{}`), "nothing")
	poll, prio = settings()
	assert.Equal(t, 90*time.Minute, poll)
	assert.Equal(t, 7, prio)

	// Every invalid member at once, with an unknown param (listed first, by parameter).
	w := patch("/api/v1/sources/1?page=1", `{"poll_interval":"1s","priority":1001}`)
	got := problemPointers(t, w, "invalid")
	assert.Equal(t, []string{"", "/poll_interval", "/priority"}, slices.Sorted(maps.Keys(got)))
	assert.Equal(t, runtime.DetailUnknownParam.Message(), got[""])
	for ptr, d := range got {
		assert.False(t, isHash(t, d), "%s: a catalogue detail passes unhashed", ptr)
	}
	poll, prio = settings()
	assert.Equal(t, 90*time.Minute, poll, "an invalid request changes nothing")
	assert.Equal(t, 7, prio)

	w = patch("/api/v1/sources/99", `{"priority":1}`)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, runtime.DetailNoSource.Message(), testutil.DecodeProblem(t, w).Detail)
	testutil.RequireParamProblem(t, patch("/api/v1/sources/x", `{}`), dto.ParamID, "bad id")
	for _, body := range []string{``, `{"priority":"1"}`, `{"poll_interval":3600}`, `{"name":"x"}`} {
		w := patch("/api/v1/sources/1", body)
		require.Equal(t, http.StatusBadRequest, w.Code, body)
		assert.Equal(t, dto.TypeInvalidBody, testutil.DecodeProblem(t, w).Type, body)
	}
}

// The admin source actions do not exist on the public listener: the action paths are 404, and
// PATCH and DELETE on a source are 405 (the path has GET only there).
func TestSourceAdminActionsNotPublic(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	h := a.Public()
	for _, action := range []string{"confirm", "pause", "resume", "cancel", "refresh"} {
		w := postJSON(t, h, "/api/v1/sources/1/"+action, `{}`)
		require.Equal(t, http.StatusNotFound, w.Code, action)
		assert.Equal(t, dto.TypeNotFound, testutil.DecodeProblem(t, w).Type, action)
	}
	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		w := sendJSON(t, h, method, "/api/v1/sources/1", `{"priority":9}`)
		require.Equal(t, http.StatusMethodNotAllowed, w.Code, method)
		assert.Equal(t, []string{http.MethodGet}, w.Header().Values("Allow"), method)
	}
	s := getJSON[dto.Source](t, h, "/api/v1/sources/1")
	assert.Equal(t, 0, s.Priority, "nothing changed")
	assert.NotZero(t, s.Backfill.RetryAt, "not refreshed")
}

// The access log names the numeric path ID, never the response's content.
func TestSourceAccessLog(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	w := testutil.Do(t, a.Public(), httptest.NewRequest(http.MethodGet, "/api/v1/source-adds/2", nil))
	require.Equal(t, http.StatusOK, w.Code)
	l := testutil.AccessLine(t, logs)
	assert.Equal(t, "/api/v1/source-adds/{job_id}", l["route"])
	assert.InDelta(t, 2, l["path_job_id"], 0)
}
