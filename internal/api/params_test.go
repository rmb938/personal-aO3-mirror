package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireAPIError asserts err is an *Error with the code and field.
func requireAPIError(t *testing.T, err error, code Code, field string) {
	t.Helper()
	e, ok := errors.AsType[*Error](err)
	require.True(t, ok, "want *Error, got %v", err)
	assert.Equal(t, code, e.Code)
	assert.Equal(t, field, e.Field)
}

func TestParseQuery(t *testing.T) {
	parse := func(raw string, allowed ...string) (query, error) {
		r := httptest.NewRequest(http.MethodGet, "/x?"+raw, nil)
		return parseQuery(r, allowed...)
	}

	q, err := parse("page=2&per_page=10", "page", "per_page")
	require.NoError(t, err)
	assert.Equal(t, query{"page": "2", "per_page": "10"}, q)

	q, err = parse("")
	require.NoError(t, err)
	assert.Empty(t, q)

	// The reveal param is allowed everywhere but not handed to the handler.
	q, err = parse(RevealParam+"=1&page=1", "page")
	require.NoError(t, err)
	assert.Equal(t, query{"page": "1"}, q)

	_, err = parse("SENTINEL=1", "page")
	requireAPIError(t, err, CodeUnknownParam, "")

	_, err = parse("page=1&page=2", "page")
	requireAPIError(t, err, CodeDuplicateParam, "page")

	_, err = parse(RevealParam + "=1&" + RevealParam + "=1")
	requireAPIError(t, err, CodeDuplicateParam, RevealParam)

	_, err = parse("page=%zz", "page")
	requireAPIError(t, err, CodeBadRequest, "")

	_, err = parse("page=1;per_page=2", "page", "per_page")
	requireAPIError(t, err, CodeBadRequest, "")

	many := strings.Repeat("a=1&", maxQueryParams) + "a=1"
	_, err = parse(many, "a")
	requireAPIError(t, err, CodeBadRequest, "")
}

func TestParseInt(t *testing.T) {
	ok := map[string]int64{"1": 1, "42": 42, "007": 7, "100": 100}
	for s, want := range ok {
		v, err := parseInt("page", s, 1, 100)
		require.NoError(t, err, s)
		assert.Equal(t, want, v, s)
	}
	for _, s := range []string{"", "0", "101", "+5", "-1", "1.5", "1e3", " 1", "1 ", "0x10", "abc",
		"99999999999999999999"} {
		_, err := parseInt("page", s, 1, 100)
		requireAPIError(t, err, CodeInvalidParam, "page")
	}

	v, err := parseID("id", "9007199254740991")
	require.NoError(t, err)
	assert.Equal(t, int64(MaxID), v)
	_, err = parseID("id", "9007199254740992")
	requireAPIError(t, err, CodeInvalidParam, "id")
	_, err = parseID("id", "0")
	requireAPIError(t, err, CodeInvalidParam, "id")
}

func TestPage(t *testing.T) {
	cases := []struct {
		q          query
		page, per  int64
		badField   string
		wantOffset int64
	}{
		{q: query{}, page: 1, per: defaultPerPage},
		{q: query{"page": "3", "per_page": "50"}, page: 3, per: 50, wantOffset: 100},
		{q: query{"page": "1001", "per_page": "100"}, page: 1001, per: 100, wantOffset: maxOffset},
		{q: query{"page": "1002", "per_page": "100"}, badField: "page"},
		{q: query{"page": "100001", "per_page": "1"}, page: 100001, per: 1, wantOffset: maxOffset},
		{q: query{"page": "100002", "per_page": "1"}, badField: "page"},
		{q: query{"page": "9223372036854775807"}, badField: "page"},
		{q: query{"page": "0"}, badField: "page"},
		{q: query{"per_page": "0"}, badField: "per_page"},
		{q: query{"per_page": "101"}, badField: "per_page"},
	}
	for _, c := range cases {
		p, err := c.q.page()
		if c.badField != "" {
			requireAPIError(t, err, CodeInvalidParam, c.badField)
			continue
		}
		require.NoError(t, err, c.q)
		assert.Equal(t, Page{Page: c.page, PerPage: c.per}, p)
		assert.Equal(t, c.wantOffset, p.Offset())
		assert.LessOrEqual(t, p.Offset(), int64(maxOffset))
	}
}

func TestCheckText(t *testing.T) {
	require.NoError(t, checkText("q", "hello wörld", 11))
	require.NoError(t, checkText("q", "", 1))
	for _, s := range []string{"\x00", "a\nb", "tab\t", "\u0085", "bad\xff", "twelve runes"} {
		requireAPIError(t, checkText("q", s, 11), CodeInvalidParam, "q")
	}
}
