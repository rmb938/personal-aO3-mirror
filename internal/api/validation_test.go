package api

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

func TestValidation(t *testing.T) {
	var v validation
	require.NoError(t, v.err(), "nothing invalid: no error")

	v.param("page", detailPage)
	v.member(detailText, "filters", "2", "tag_id")
	p := asAPIError(t, v.err()).problem()
	assert.Equal(t, dto.TypeValidation, p.Type)
	assert.Equal(t, detailInvalidRequest.msg, p.Detail)
	assert.Equal(t, []dto.ProblemError{
		{Detail: detailPage.msg, Parameter: dto.ParamPage},
		{Detail: detailText.msg, Pointer: "/filters/2/tag_id"},
	}, p.Errors)
}

func TestPointer(t *testing.T) {
	assert.Equal(t, "/url", pointer("url"))
	assert.Equal(t, "/a/0/b", pointer("a", "0", "b"))
}

// pointerRequest stands in for a request DTO: the enforcer passes pointers into it.
type pointerRequest struct {
	URL   string `json:"url"`
	Items []struct {
		TagID int64 `json:"tag_id"`
	} `json:"items"`
}

// With a request type registered, a member pointer passes the enforcer unhashed and anything
// else in pointer is hashed (the API registers requestTypes; this uses a test type).
func TestProblemPointerEnforced(t *testing.T) {
	h, err := redact.NewHasher(0, nil)
	require.NoError(t, err)
	reg := redact.NewRegistry()
	require.NoError(t, reg.RegisterTypes(dto.Problem{}))
	require.NoError(t, reg.RegisterFieldEnum(dto.ProblemError{}, "detail", detailCatalogue()...))
	require.NoError(t, reg.RegisterPointerField(dto.ProblemError{}, "pointer", pointerRequest{}))
	enf, err := redact.NewEnforcer(reg, h)
	require.NoError(t, err)

	for ptr, pass := range map[string]bool{"/url": true, "/items/3/tag_id": true, "/nope": false, "/SENTINEL": false} {
		var v validation
		v.items = append(v.items, ErrorItem{Pointer: ptr, Detail: detailText})
		body, err := json.Marshal(asAPIError(t, v.err()).problem())
		require.NoError(t, err)
		res, err := enf.Enforce(redact.Hashed, reflect.TypeFor[dto.Problem](), body)
		require.NoError(t, err)
		var out dto.Problem
		require.NoError(t, json.Unmarshal(res.Body, &out))
		assert.Equal(t, pass, out.Errors[0].Pointer == ptr, ptr)
		assert.Equal(t, detailText.msg, out.Errors[0].Detail)
	}
}

func TestURLDetails(t *testing.T) {
	for _, r := range ao3url.Reasons() {
		d, ok := urlDetails[r]
		require.True(t, ok, r)
		assert.Equal(t, r.Hint(), d.msg)
		assert.Contains(t, detailCatalogue(), d.msg)
	}
	_, err := ao3url.Parse("/works/search")
	d, ok := urlDetail(fmt.Errorf("add: %w", err))
	require.True(t, ok)
	assert.Equal(t, urlDetails[ao3url.ReasonWorkSearch], d)
	_, ok = urlDetail(errors.New("other"))
	assert.False(t, ok)
}
