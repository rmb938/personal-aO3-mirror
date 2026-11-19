package problem

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/api/redact"
)

// asError requires err to be an *Error and returns it.
func asError(t *testing.T, err error) *Error {
	t.Helper()
	e, ok := errors.AsType[*Error](err)
	require.True(t, ok, "want *Error, got %v", err)
	return e
}

var detailTest = NewDetail("test detail")

func TestValidation(t *testing.T) {
	var v Validation
	require.NoError(t, v.Err(), "nothing invalid: no error")
	assert.Zero(t, v.Len())

	v.Param("page", detailTest)
	v.Member(DetailText, "filters", "2", "tag_id")
	assert.Equal(t, 2, v.Len())
	p := asError(t, v.Err()).Problem()
	assert.Equal(t, dto.TypeValidation, p.Type)
	assert.Equal(t, DetailInvalidRequest.Message(), p.Detail)
	assert.Equal(t, []dto.ProblemError{
		{Detail: detailTest.Message(), Parameter: dto.ParamPage},
		{Detail: DetailText.Message(), Pointer: "/filters/2/tag_id"},
	}, p.Errors)
}

func TestPointer(t *testing.T) {
	assert.Equal(t, "/url", pointer("url"))
	assert.Equal(t, "/a/0/b", pointer("a", "0", "b"))
}

func TestProblemParameter(t *testing.T) {
	p := ForParam("per_page", detailTest).Problem()
	assert.Equal(t, dto.TypeValidation, p.Type)
	assert.Equal(t, DetailInvalidRequest.Message(), p.Detail)
	assert.Equal(t, []dto.ProblemError{{Detail: detailTest.Message(), Parameter: dto.ParamPerPage}}, p.Errors)

	// A name the API doesn't declare is never echoed.
	p = ForParam("SENTINEL-field", detailTest).Problem()
	assert.Equal(t, []dto.ProblemError{{Detail: detailTest.Message()}}, p.Errors)
	assert.NotContains(t, ForParam("SENTINEL-field", detailTest).Error(), "SENTINEL")
	assert.Equal(t, "api: validation-error (per_page)", ForParam("per_page", detailTest).Error())
}

func TestProblemStatusAndUnknownType(t *testing.T) {
	p := (&Error{Type: dto.TypeRevealRequired, Status: 403}).Problem()
	assert.Equal(t, 403, p.Status)
	assert.Equal(t, dto.TypeInternal, (&Error{Type: "made_up"}).Problem().Type)

	// Empty detail and errors are omitted.
	body, err := json.Marshal(New(dto.TypeNotFound).Problem())
	require.NoError(t, err)
	assert.NotContains(t, string(body), "detail")
	assert.NotContains(t, string(body), "errors")
	assert.Equal(t, detailTest, WithDetail(dto.TypeNotFound, detailTest).Detail)
}

// The detail catalogue holds only fixed messages: none is empty or repeated by accident (a
// repeat would mean two names for one message).
func TestDetailCatalogue(t *testing.T) {
	assert.Len(t, Catalogue(), len(details), "no message is defined twice")
	for _, m := range details {
		assert.NotEmpty(t, m)
		assert.NotContains(t, m, "\n")
	}
	assert.Equal(t, "must be one of: a, b", oneOfFixture.Message())
}

type enumFixture string

func (enumFixture) EnumValues() []string { return []string{"a", "b"} }

var oneOfFixture = OneOf(enumFixture(""))

// pointerRequest stands in for a request DTO: the enforcer passes pointers into it.
type pointerRequest struct {
	URL   string `json:"url"`
	Items []struct {
		TagID int64 `json:"tag_id"`
	} `json:"items"`
}

// With a request type registered, a member pointer passes the enforcer unhashed and anything
// else in pointer is hashed (the API registers its request types; this uses a test type).
func TestProblemPointerEnforced(t *testing.T) {
	h, err := redact.NewHasher(0, nil)
	require.NoError(t, err)
	reg := redact.NewRegistry()
	require.NoError(t, reg.RegisterTypes(dto.Problem{}))
	require.NoError(t, reg.RegisterFieldEnum(dto.ProblemError{}, "detail", Catalogue()...))
	require.NoError(t, reg.RegisterPointerField(dto.ProblemError{}, "pointer", pointerRequest{}))
	enf, err := redact.NewEnforcer(reg, h)
	require.NoError(t, err)

	for ptr, pass := range map[string]bool{"/url": true, "/items/3/tag_id": true, "/nope": false, "/SENTINEL": false} {
		var v Validation
		v.items = append(v.items, Item{Pointer: ptr, Detail: DetailText})
		body, err := json.Marshal(asError(t, v.Err()).Problem())
		require.NoError(t, err)
		res, err := enf.Enforce(redact.Hashed, reflect.TypeFor[dto.Problem](), body)
		require.NoError(t, err)
		var out dto.Problem
		require.NoError(t, json.Unmarshal(res.Body, &out))
		assert.Equal(t, pass, out.Errors[0].Pointer == ptr, ptr)
		assert.Equal(t, DetailText.Message(), out.Errors[0].Detail)
	}
}

func TestURLDetails(t *testing.T) {
	for _, r := range ao3url.Reasons() {
		d, ok := urlDetails[r]
		require.True(t, ok, r)
		assert.Equal(t, r.Hint(), d.Message())
		assert.Contains(t, Catalogue(), d.Message())
	}
	_, err := ao3url.Parse("/works/search")
	d, ok := URLDetail(fmt.Errorf("add: %w", err))
	require.True(t, ok)
	assert.Equal(t, urlDetails[ao3url.ReasonWorkSearch], d)
	_, ok = URLDetail(errors.New("other"))
	assert.False(t, ok)
}

func TestValidText(t *testing.T) {
	assert.True(t, ValidText("hello wörld", 11))
	assert.True(t, ValidText("", 1))
	for _, s := range []string{"\x00", "a\nb", "tab\t", "\u0085", "bad\xff", "twelve runes"} {
		assert.False(t, ValidText(s, 11), "%q", s)
	}
}
