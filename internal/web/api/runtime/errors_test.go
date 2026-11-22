package runtime

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

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/redact"
)

func TestProblemFor(t *testing.T) {
	cases := []struct {
		err      error
		typ      dto.ProblemType
		status   int
		internal bool
	}{
		{problem.New(dto.TypeBadRequest), dto.TypeBadRequest, http.StatusBadRequest, false},
		{fmt.Errorf("wrap: %w", problem.ForParam("page", DetailPage)), dto.TypeValidation, http.StatusUnprocessableEntity, false},
		{&problem.Error{Type: dto.TypeRevealRequired, Status: http.StatusForbidden}, dto.TypeRevealRequired, http.StatusForbidden, false},
		{&problem.Error{Type: "made_up"}, dto.TypeInternal, http.StatusInternalServerError, true},
		{problem.New(dto.TypeInternal), dto.TypeInternal, http.StatusInternalServerError, true},
		{store.ErrNoSource, dto.TypeNotFound, http.StatusNotFound, false},
		{fmt.Errorf("get: %w", store.ErrNoJob), dto.TypeNotFound, http.StatusNotFound, false},
		{store.ErrNoSourceAdd, dto.TypeNotFound, http.StatusNotFound, false},
		{store.ErrNoWork, dto.TypeNotFound, http.StatusNotFound, false},
		{store.ErrNoChapter, dto.TypeNotFound, http.StatusNotFound, false},
		{fmt.Errorf("read: %w", controllers.ErrWorkTooLarge), dto.TypeWorkTooLarge, http.StatusUnprocessableEntity, false},
		{store.ErrNoSeries, dto.TypeNotFound, http.StatusNotFound, false},
		{fmt.Errorf("get: %w", store.ErrNoTag), dto.TypeNotFound, http.StatusNotFound, false},
		{controllers.ErrNoMedium, dto.TypeNotFound, http.StatusNotFound, false},
		{store.ErrNoRawPage, dto.TypeNotFound, http.StatusNotFound, false},
		{store.ErrNoUser, dto.TypeNotFound, http.StatusNotFound, false},
		{fmt.Errorf("revoke: %w", store.ErrNoInvite), dto.TypeNotFound, http.StatusNotFound, false},
		{store.ErrNoSession, dto.TypeNotFound, http.StatusNotFound, false},
		{store.ErrStateConflict, dto.TypeStateConflict, http.StatusConflict, false},
		{store.ErrSourceExists, dto.TypeAlreadyExists, http.StatusConflict, false},
		{fmt.Errorf("add: %w", controllers.ExistsError{ID: 3}), dto.TypeAlreadyExists, http.StatusConflict, false},
		{context.DeadlineExceeded, dto.TypeUnavailable, http.StatusServiceUnavailable, false},
		{context.Canceled, dto.TypeUnavailable, http.StatusServiceUnavailable, false},
		{errors.New("boom"), dto.TypeInternal, http.StatusInternalServerError, true},
	}
	for _, c := range cases {
		t.Run(c.err.Error(), func(t *testing.T) {
			p, internal := ProblemFor(c.err)
			assert.Equal(t, c.typ, p.Type)
			assert.Equal(t, c.status, p.Status)
			info, ok := c.typ.Info()
			require.True(t, ok)
			assert.Equal(t, info.Title, p.Title)
			assert.Equal(t, c.internal, internal)
		})
	}
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
	raw, err := os.ReadFile("../../../../docs/api/problems.md")
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

// Every problem the API can produce passes the enforcer in hashed mode unchanged: its strings
// are all registered enum values or catalogue messages (problem.Catalogue, with the api
// package's details added).
func TestProblemsPassEnforcerUnhashed(t *testing.T) {
	h, err := redact.NewHasher(0, nil)
	require.NoError(t, err)
	enf, err := NewEnforcer(h, "test-version")
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
	cat := problem.Catalogue()
	assert.Contains(t, cat, DetailNoRoute.Message(), "the api package's details are in the catalogue")
	for _, s := range dto.ProblemType("").EnumValues() {
		info, _ := dto.ProblemType(s).Info()
		for _, m := range cat {
			check(dto.Problem{Type: dto.ProblemType(s), Title: info.Title, Status: info.Status, Detail: m})
		}
	}
	for _, f := range dto.ParamName("").EnumValues() {
		for _, m := range cat {
			check(dto.Problem{Type: dto.TypeValidation, Title: "Validation Error", Status: 422,
				Detail: problem.DetailInvalidRequest.Message(), Errors: []dto.ProblemError{{Detail: m, Parameter: dto.ParamName(f)}}})
		}
	}

	// A message outside the catalogue is hashed.
	body, err := json.Marshal(dto.Problem{Type: dto.TypeNotFound, Title: "Not Found", Status: 404, Detail: "SENTINEL-x",
		Errors: []dto.ProblemError{{Detail: "SENTINEL-y"}}})
	require.NoError(t, err)
	res, err := enf.Enforce(redact.Hashed, reflect.TypeFor[dto.Problem](), body)
	require.NoError(t, err)
	assert.NotContains(t, string(res.Body), "SENTINEL")
}
