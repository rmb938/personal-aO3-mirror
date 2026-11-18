package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/api/redact"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

func TestProblemFor(t *testing.T) {
	cases := []struct {
		err      error
		code     Code
		status   int
		internal bool
	}{
		{errCode(CodeBadRequest), CodeBadRequest, http.StatusBadRequest, false},
		{fmt.Errorf("wrap: %w", errField(CodeInvalidParam, "page")), CodeInvalidParam, http.StatusBadRequest, false},
		{&Error{Code: CodeRevealRequired, Status: http.StatusForbidden}, CodeRevealRequired, http.StatusForbidden, false},
		{&Error{Code: "made_up"}, CodeInternal, http.StatusInternalServerError, true},
		{errCode(CodeInternal), CodeInternal, http.StatusInternalServerError, true},
		{store.ErrNoSource, CodeNotFound, http.StatusNotFound, false},
		{fmt.Errorf("get: %w", store.ErrNoJob), CodeNotFound, http.StatusNotFound, false},
		{store.ErrNoSourceAdd, CodeNotFound, http.StatusNotFound, false},
		{store.ErrNoWork, CodeNotFound, http.StatusNotFound, false},
		{store.ErrNoSeries, CodeNotFound, http.StatusNotFound, false},
		{store.ErrNoTag, CodeNotFound, http.StatusNotFound, false},
		{store.ErrNoRawPage, CodeNotFound, http.StatusNotFound, false},
		{store.ErrStateConflict, CodeConflict, http.StatusConflict, false},
		{store.ErrSourceExists, CodeExists, http.StatusConflict, false},
		{fmt.Errorf("add: %w", controllers.ExistsError{ID: 3}), CodeExists, http.StatusConflict, false},
		{context.DeadlineExceeded, CodeUnavailable, http.StatusServiceUnavailable, false},
		{context.Canceled, CodeUnavailable, http.StatusServiceUnavailable, false},
		{errors.New("boom"), CodeInternal, http.StatusInternalServerError, true},
	}
	for _, c := range cases {
		t.Run(c.err.Error(), func(t *testing.T) {
			p, internal := problemFor(c.err)
			assert.Equal(t, string(c.code), p.Code)
			assert.Equal(t, c.status, p.Status)
			assert.Equal(t, codeInfo[c.code].title, p.Title)
			assert.Equal(t, c.internal, internal)
		})
	}
}

func TestProblemField(t *testing.T) {
	p := errField(CodeInvalidParam, "per_page").problem()
	assert.Equal(t, "per_page", p.Field)

	// A name the API doesn't declare is never echoed.
	p = errField(CodeInvalidParam, "SENTINEL-field").problem()
	assert.Empty(t, p.Field)
}

func TestCodeInfoComplete(t *testing.T) {
	titles := map[string]Code{}
	for c, info := range codeInfo {
		assert.NotZero(t, info.status, c)
		assert.NotEmpty(t, info.title, c)
		assert.Regexp(t, `^[a-z][a-z_]*$`, string(c))
		if prev, dup := titles[info.title]; dup {
			t.Errorf("codes %s and %s share a title", prev, c)
		}
		titles[info.title] = c
	}
}

// Every problem the API can produce passes the enforcer in hashed mode unchanged: its strings
// are all registered enum values.
func TestProblemsPassEnforcerUnhashed(t *testing.T) {
	h, err := redact.NewHasher(0, nil)
	require.NoError(t, err)
	enf, err := newEnforcer(h, "test-version")
	require.NoError(t, err)
	for c := range codeInfo {
		for f := range knownFields {
			body, err := json.Marshal(errField(c, f).problem())
			require.NoError(t, err)
			res, err := enf.Enforce(redact.Hashed, body)
			require.NoError(t, err)
			assert.JSONEq(t, string(body), string(res.Body))
		}
	}
	// The zero Field is omitted.
	body, err := json.Marshal(dto.Problem{Code: "x", Title: "y", Status: 1})
	require.NoError(t, err)
	assert.NotContains(t, string(body), "field")
}
