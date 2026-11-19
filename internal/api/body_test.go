package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/api/problem"
)

type testBody struct {
	URL      string `json:"url"`
	Priority int    `json:"priority"`
	Tags     []int  `json:"tags"`

	validated bool
}

var detailTestPriority = problem.NewDetail("test: must not be negative")

func (b *testBody) Validate(v *problem.Validation) {
	b.validated = true
	if b.Priority < 0 {
		v.Member(detailTestPriority, "priority")
	}
}

// decodeTest decodes body through handle, so the body cap applies as in production. The error
// is decodeJSON's, else the recorded validation error.
func decodeTest(t *testing.T, contentType, body string) (testBody, error) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	var dst testBody
	var derr error
	handle(discardLogger(), func(r *http.Request) (any, error) {
		var v problem.Validation
		derr = decodeJSON(r, &dst, &v)
		if derr == nil {
			derr = v.Err()
		} else {
			assert.False(t, dst.validated, "an unreadable body is not validated")
		}
		return nil, derr
	})(httptest.NewRecorder(), r)
	return dst, derr
}

func TestDecodeJSONValidates(t *testing.T) {
	got, err := decodeTest(t, "application/json", `{"url":"u","priority":-1}`)
	assert.True(t, got.validated)
	pe := asAPIError(t, err)
	assert.Equal(t, dto.TypeValidation, pe.Type)
	assert.Equal(t, []problem.Item{{Pointer: "/priority", Detail: detailTestPriority}}, pe.Errors)
}

func TestDecodeJSON(t *testing.T) {
	got, err := decodeTest(t, "application/json", `{"url":"u","priority":2,"tags":[1,2]}`)
	require.NoError(t, err)
	assert.Equal(t, testBody{URL: "u", Priority: 2, Tags: []int{1, 2}, validated: true}, got)

	_, err = decodeTest(t, "application/json; charset=UTF-8", ` {"url":"u"} `)
	require.NoError(t, err)

	bad := map[string]string{
		"empty":          ``,
		"not json":       `nope`,
		"unknown field":  `{"url":"u","SENTINEL":1}`,
		"duplicate key":  `{"url":"a","url":"b"}`,
		"trailing value": `{"url":"u"}{}`,
		"trailing junk":  `{"url":"u"} x`,
		"invalid utf8":   "{\"url\":\"\xff\"}",
		"wrong type":     `{"priority":"high"}`,
		"case mismatch":  `{"URL":"u"}`,
		"array":          `[]`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			_, err := decodeTest(t, "application/json", body)
			requireAPIError(t, err, dto.TypeInvalidBody)
		})
	}
}

func TestDecodeJSONContentType(t *testing.T) {
	for _, ct := range []string{"", "text/plain", "application/json; charset=latin1",
		"application/json; foo=bar", "application/jsonx", "application/problem+json", "%%%"} {
		_, err := decodeTest(t, ct, `{}`)
		requireAPIError(t, err, dto.TypeUnsupportedMediaType)
	}

	// Two Content-Type headers are refused.
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{}`))
	r.Header.Add("Content-Type", "application/json")
	r.Header.Add("Content-Type", "application/json")
	requireAPIError(t, decodeJSON(r, &testBody{}, &problem.Validation{}), dto.TypeUnsupportedMediaType)
}

func TestDecodeJSONTooLarge(t *testing.T) {
	pad := strings.Repeat(" ", maxBodyBytes)
	_, err := decodeTest(t, "application/json", `{"url":"u"}`+pad)
	requireAPIError(t, err, dto.TypeBodyTooLarge)

	// Exactly at the cap is fine.
	body := `{"url":"u"}`
	_, err = decodeTest(t, "application/json", body+strings.Repeat(" ", maxBodyBytes-len(body)))
	require.NoError(t, err)
}
