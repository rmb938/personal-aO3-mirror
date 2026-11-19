package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
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
		typ      dto.ProblemType
		status   int
		internal bool
	}{
		{errType(dto.TypeBadRequest), dto.TypeBadRequest, http.StatusBadRequest, false},
		{fmt.Errorf("wrap: %w", errParam("page", detailPage)), dto.TypeValidation, http.StatusUnprocessableEntity, false},
		{&Error{Type: dto.TypeRevealRequired, Status: http.StatusForbidden}, dto.TypeRevealRequired, http.StatusForbidden, false},
		{&Error{Type: "made_up"}, dto.TypeInternal, http.StatusInternalServerError, true},
		{errType(dto.TypeInternal), dto.TypeInternal, http.StatusInternalServerError, true},
		{store.ErrNoSource, dto.TypeNotFound, http.StatusNotFound, false},
		{fmt.Errorf("get: %w", store.ErrNoJob), dto.TypeNotFound, http.StatusNotFound, false},
		{store.ErrNoSourceAdd, dto.TypeNotFound, http.StatusNotFound, false},
		{store.ErrNoWork, dto.TypeNotFound, http.StatusNotFound, false},
		{store.ErrNoSeries, dto.TypeNotFound, http.StatusNotFound, false},
		{store.ErrNoTag, dto.TypeNotFound, http.StatusNotFound, false},
		{store.ErrNoRawPage, dto.TypeNotFound, http.StatusNotFound, false},
		{store.ErrStateConflict, dto.TypeStateConflict, http.StatusConflict, false},
		{store.ErrSourceExists, dto.TypeAlreadyExists, http.StatusConflict, false},
		{fmt.Errorf("add: %w", controllers.ExistsError{ID: 3}), dto.TypeAlreadyExists, http.StatusConflict, false},
		{context.DeadlineExceeded, dto.TypeUnavailable, http.StatusServiceUnavailable, false},
		{context.Canceled, dto.TypeUnavailable, http.StatusServiceUnavailable, false},
		{errors.New("boom"), dto.TypeInternal, http.StatusInternalServerError, true},
	}
	for _, c := range cases {
		t.Run(c.err.Error(), func(t *testing.T) {
			p, internal := problemFor(c.err)
			assert.Equal(t, c.typ, p.Type)
			assert.Equal(t, c.status, p.Status)
			info, ok := c.typ.Info()
			require.True(t, ok)
			assert.Equal(t, info.Title, p.Title)
			assert.Equal(t, c.internal, internal)
		})
	}
}

func TestProblemParameter(t *testing.T) {
	p := errParam("per_page", detailPerPage).problem()
	assert.Equal(t, dto.TypeValidation, p.Type)
	assert.Equal(t, detailInvalidParams.msg, p.Detail)
	assert.Equal(t, []dto.ProblemError{{Detail: detailPerPage.msg, Parameter: dto.ParamPerPage}}, p.Errors)

	// A name the API doesn't declare is never echoed.
	p = errParam("SENTINEL-field", detailUnknownParam).problem()
	assert.Equal(t, []dto.ProblemError{{Detail: detailUnknownParam.msg}}, p.Errors)
	assert.NotContains(t, errParam("SENTINEL-field", detailUnknownParam).Error(), "SENTINEL")
}

func TestProblemTypesComplete(t *testing.T) {
	titles := map[dto.ProblemTitle]dto.ProblemType{}
	for _, s := range dto.ProblemType("").EnumValues() {
		typ := dto.ProblemType(s)
		info, ok := typ.Info()
		require.True(t, ok, typ)
		assert.NotZero(t, info.Status, typ)
		assert.NotEmpty(t, info.Title, typ)
		assert.True(t, strings.HasPrefix(s, dto.ProblemTypeBase), s)
		assert.Regexp(t, `^[a-z][a-z-]*$`, typ.Slug())
		if prev, dup := titles[info.Title]; dup {
			t.Errorf("types %s and %s share a title", prev, typ)
		}
		titles[info.Title] = typ
	}
	assert.Len(t, dto.ProblemTitle("").EnumValues(), len(titles))
}

// docs/api/problems.md has exactly one "## <slug>" section per problem type, so every type URI
// resolves to its documentation.
func TestProblemTypesDocumented(t *testing.T) {
	raw, err := os.ReadFile("../../docs/api/problems.md")
	require.NoError(t, err)
	var documented []string
	for _, m := range regexp.MustCompile(`(?m)^## ([a-z-]+)$`).FindAllStringSubmatch(string(raw), -1) {
		documented = append(documented, m[1])
	}
	var types []string
	for _, s := range dto.ProblemType("").EnumValues() {
		types = append(types, dto.ProblemType(s).Slug())
	}
	slices.Sort(documented)
	assert.Equal(t, types, documented)
}

// The detail catalogue holds only fixed messages: none is empty or repeated by accident (a
// repeat would mean two names for one message).
func TestDetailCatalogue(t *testing.T) {
	assert.Len(t, detailCatalogue(), len(details), "no message is defined twice")
	for _, m := range details {
		assert.NotEmpty(t, m)
		assert.NotContains(t, m, "\n")
	}
}

// Every problem the API can produce passes the enforcer in hashed mode unchanged: its strings
// are all registered enum values or catalogue messages.
func TestProblemsPassEnforcerUnhashed(t *testing.T) {
	h, err := redact.NewHasher(0, nil)
	require.NoError(t, err)
	enf, err := newEnforcer(h, "test-version")
	require.NoError(t, err)
	check := func(p dto.Problem) {
		t.Helper()
		body, err := json.Marshal(p)
		require.NoError(t, err)
		res, err := enf.Enforce(redact.Hashed, reflect.TypeFor[dto.Problem](), body)
		require.NoError(t, err)
		assert.JSONEq(t, string(body), string(res.Body))
		assert.Empty(t, res.OffEnum)
	}
	for _, s := range dto.ProblemType("").EnumValues() {
		for _, m := range details {
			check((&Error{Type: dto.ProblemType(s), Detail: detail{m}}).problem())
		}
	}
	for _, f := range dto.ParamName("").EnumValues() {
		for _, m := range details {
			check(errParam(f, detail{m}).problem())
		}
	}

	// A message outside the catalogue is hashed.
	body, err := json.Marshal(dto.Problem{Type: dto.TypeNotFound, Title: "Not Found", Status: 404, Detail: "SENTINEL-x",
		Errors: []dto.ProblemError{{Detail: "SENTINEL-y"}}})
	require.NoError(t, err)
	res, err := enf.Enforce(redact.Hashed, reflect.TypeFor[dto.Problem](), body)
	require.NoError(t, err)
	assert.NotContains(t, string(res.Body), "SENTINEL")

	// Empty detail and errors are omitted.
	body, err = json.Marshal(errType(dto.TypeNotFound).problem())
	require.NoError(t, err)
	assert.NotContains(t, string(body), "detail")
	assert.NotContains(t, string(body), "errors")
}
