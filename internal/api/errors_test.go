package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
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
		code     dto.ProblemCode
		status   int
		internal bool
	}{
		{errCode(dto.CodeBadRequest), dto.CodeBadRequest, http.StatusBadRequest, false},
		{fmt.Errorf("wrap: %w", errField(dto.CodeInvalidParam, "page")), dto.CodeInvalidParam, http.StatusBadRequest, false},
		{&Error{Code: dto.CodeRevealRequired, Status: http.StatusForbidden}, dto.CodeRevealRequired, http.StatusForbidden, false},
		{&Error{Code: "made_up"}, dto.CodeInternal, http.StatusInternalServerError, true},
		{errCode(dto.CodeInternal), dto.CodeInternal, http.StatusInternalServerError, true},
		{store.ErrNoSource, dto.CodeNotFound, http.StatusNotFound, false},
		{fmt.Errorf("get: %w", store.ErrNoJob), dto.CodeNotFound, http.StatusNotFound, false},
		{store.ErrNoSourceAdd, dto.CodeNotFound, http.StatusNotFound, false},
		{store.ErrNoWork, dto.CodeNotFound, http.StatusNotFound, false},
		{store.ErrNoSeries, dto.CodeNotFound, http.StatusNotFound, false},
		{store.ErrNoTag, dto.CodeNotFound, http.StatusNotFound, false},
		{store.ErrNoRawPage, dto.CodeNotFound, http.StatusNotFound, false},
		{store.ErrStateConflict, dto.CodeConflict, http.StatusConflict, false},
		{store.ErrSourceExists, dto.CodeExists, http.StatusConflict, false},
		{fmt.Errorf("add: %w", controllers.ExistsError{ID: 3}), dto.CodeExists, http.StatusConflict, false},
		{context.DeadlineExceeded, dto.CodeUnavailable, http.StatusServiceUnavailable, false},
		{context.Canceled, dto.CodeUnavailable, http.StatusServiceUnavailable, false},
		{errors.New("boom"), dto.CodeInternal, http.StatusInternalServerError, true},
	}
	for _, c := range cases {
		t.Run(c.err.Error(), func(t *testing.T) {
			p, internal := problemFor(c.err)
			assert.Equal(t, c.code, p.Code)
			assert.Equal(t, c.status, p.Status)
			info, ok := c.code.Info()
			require.True(t, ok)
			assert.Equal(t, info.Title, p.Title)
			assert.Equal(t, c.internal, internal)
		})
	}
}

func TestProblemField(t *testing.T) {
	p := errField(dto.CodeInvalidParam, "per_page").problem()
	assert.Equal(t, dto.ParamPerPage, p.Field)

	// A name the API doesn't declare is never echoed.
	p = errField(dto.CodeInvalidParam, "SENTINEL-field").problem()
	assert.Empty(t, p.Field)
}

func TestProblemCodesComplete(t *testing.T) {
	titles := map[dto.ProblemTitle]dto.ProblemCode{}
	for _, s := range dto.ProblemCode("").EnumValues() {
		c := dto.ProblemCode(s)
		info, ok := c.Info()
		require.True(t, ok, c)
		assert.NotZero(t, info.Status, c)
		assert.NotEmpty(t, info.Title, c)
		assert.Regexp(t, `^[a-z][a-z_]*$`, s)
		if prev, dup := titles[info.Title]; dup {
			t.Errorf("codes %s and %s share a title", prev, c)
		}
		titles[info.Title] = c
	}
	assert.Len(t, dto.ProblemTitle("").EnumValues(), len(titles))
}

// Every problem the API can produce passes the enforcer in hashed mode unchanged: its strings
// are all registered enum values.
func TestProblemsPassEnforcerUnhashed(t *testing.T) {
	h, err := redact.NewHasher(0, nil)
	require.NoError(t, err)
	enf, err := newEnforcer(h, "test-version")
	require.NoError(t, err)
	for _, c := range dto.ProblemCode("").EnumValues() {
		for _, f := range dto.ParamName("").EnumValues() {
			body, err := json.Marshal(errField(dto.ProblemCode(c), f).problem())
			require.NoError(t, err)
			res, err := enf.Enforce(redact.Hashed, reflect.TypeFor[dto.Problem](), body)
			require.NoError(t, err)
			assert.JSONEq(t, string(body), string(res.Body))
		}
	}
	// The zero Field is omitted.
	body, err := json.Marshal(dto.Problem{Code: "x", Title: "y", Status: 1})
	require.NoError(t, err)
	assert.NotContains(t, string(body), "field")
}
