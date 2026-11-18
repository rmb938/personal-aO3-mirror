package redact

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Synthetic DTOs for the enforcer tests.
type testTag struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type testWork struct {
	ID          int64      `json:"id"`
	Title       string     `json:"title"`
	State       string     `json:"state"`
	Complete    bool       `json:"complete"`
	Words       *int64     `json:"words"`
	Tags        []testTag  `json:"tags"`
	Kinds       []string   `json:"kinds"`
	UpdatedAt   time.Time  `json:"updated_at"`
	PublishedOn string     `json:"published_on"`
	Notes       []string   `json:"notes"`
	Matrix      [][]string `json:"matrix"`
}

type testList struct {
	Items   []testWork `json:"items"`
	Page    int        `json:"page"`
	PerPage int        `json:"per_page"`
	Total   int        `json:"total"`
}

func newTestEnforcer(tb testing.TB) (*Enforcer, *Hasher) {
	tb.Helper()
	h, err := NewHasher(0, nil)
	require.NoError(tb, err)
	r := NewRegistry()
	require.NoError(tb, r.RegisterTypes(testList{}))
	require.NoError(tb, r.RegisterEnum("state", "queued", "running", "done"))
	require.NoError(tb, r.RegisterEnum("type", "fandom", "character"))
	require.NoError(tb, r.RegisterEnum("kinds", "a", "b"))
	e, err := NewEnforcer(r, h)
	require.NoError(tb, err)
	return e, h
}

func enforceHashed(t *testing.T, e *Enforcer, in string) string {
	t.Helper()
	res, err := e.Enforce(Hashed, []byte(in))
	require.NoError(t, err)
	require.True(t, json.Valid(res.Body), string(res.Body))
	return string(res.Body)
}

func TestEnforceRules(t *testing.T) {
	e, h := newTestEnforcer(t)
	hs := h.Current().Hash
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }

	cases := []struct {
		name, in, want string
	}{
		{"scalars pass", `{"id":12,"page":-1.5e3,"complete":true,"words":null}`, `{"id":12,"page":-1.5e3,"complete":true,"words":null}`},
		{"big number verbatim", `{"id":123456789012345678901234567890}`, `{"id":123456789012345678901234567890}`},
		{"free string hashed", `{"title":"SENTINEL-title"}`, `{"title":` + q(hs("SENTINEL-title")) + `}`},
		{"empty string hashed", `{"title":""}`, `{"title":` + q(hs("")) + `}`},
		{"enum in set", `{"state":"running"}`, `{"state":"running"}`},
		{"enum out of set hashed", `{"state":"SENTINEL-state"}`, `{"state":` + q(hs("SENTINEL-state")) + `}`},
		{"enum case-sensitive", `{"state":"Running"}`, `{"state":` + q(hs("Running")) + `}`},
		{"other key's enum value hashed", `{"title":"running"}`, `{"title":` + q(hs("running")) + `}`},
		{"enum inside array", `{"kinds":["a","SENTINEL-k","b"]}`, `{"kinds":["a",` + q(hs("SENTINEL-k")) + `,"b"]}`},
		{"nested arrays inherit key", `{"matrix":[["x"],[]]}`, `{"matrix":[[` + q(hs("x")) + `],[]]}`},
		{"timestamp", `{"updated_at":"2026-01-02T03:04:05Z"}`, `{"updated_at":"2026-01-02T03:04:05Z"}`},
		{"timestamp nanos", `{"updated_at":"2026-01-02T03:04:05.123456789Z"}`, `{"updated_at":"2026-01-02T03:04:05.123456789Z"}`},
		{"date", `{"published_on":"2026-02-28"}`, `{"published_on":"2026-02-28"}`},
		{"nested objects", `{"items":[{"id":1,"tags":[{"id":2,"name":"SENTINEL-tag","type":"fandom"}]}],"total":1}`,
			`{"items":[{"id":1,"tags":[{"id":2,"name":` + q(hs("SENTINEL-tag")) + `,"type":"fandom"}]}],"total":1}`},
		{"top-level string hashed", `"SENTINEL-top"`, q(hs("SENTINEL-top"))},
		{"top-level array", `[{"id":1},"x",3]`, `[{"id":1},` + q(hs("x")) + `,3]`},
		{"whitespace dropped", " {\n \"id\" : 1 ,\"kinds\":[ ] }\n", `{"id":1,"kinds":[]}`},
		{"empty object", `{}`, `{}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.JSONEq(t, c.want, enforceHashed(t, e, c.in))
			assert.Equal(t, c.want, enforceHashed(t, e, c.in), "byte-exact")
		})
	}
}

func TestEnforceDatesStrict(t *testing.T) {
	e, _ := newTestEnforcer(t)
	pass := func(key, v string) bool {
		out := enforceHashed(t, e, `{"`+key+`":"`+v+`"}`)
		return strings.Contains(out, `"`+v+`"`)
	}
	for _, v := range []string{"2026-01-02T03:04:05Z", "2026-01-02T03:04:05.5Z", "0001-01-01T00:00:00Z"} {
		assert.True(t, pass("updated_at", v), v)
	}
	for _, v := range []string{
		"2026-01-02T03:04:05+00:00", "2026-01-02T03:04:05-07:00", "2026-01-02T03:04:05",
		"2026-01-02T03:04:05.50Z", "2026-01-02 03:04:05Z", "2026-01-02t03:04:05z",
		"2026-13-02T03:04:05Z", "2026-01-02T24:00:00Z", "2026-01-02", "SENTINEL Z", "",
	} {
		assert.False(t, pass("updated_at", v), v)
	}
	for _, v := range []string{"2026-01-02", "2024-02-29"} {
		assert.True(t, pass("published_on", v), v)
	}
	for _, v := range []string{"2026-1-02", "2025-02-29", "2026-01-02T00:00:00Z", "20260102", " 2026-01-02", ""} {
		assert.False(t, pass("published_on", v), v)
	}
	// The suffix rules only apply to their own suffixes.
	assert.False(t, pass("title", "2026-01-02"))
	assert.False(t, pass("title", "2026-01-02T03:04:05Z"))
	assert.False(t, pass("updated_at", "2026-01-02"), "a date is not a timestamp")
}

func TestEnforceSameStringSameHashAcrossKeys(t *testing.T) {
	e, _ := newTestEnforcer(t)
	var out struct {
		Title string    `json:"title"`
		Notes []string  `json:"notes"`
		Tags  []testTag `json:"tags"`
	}
	require.NoError(t, json.Unmarshal([]byte(enforceHashed(t, e,
		`{"title":"SENTINEL-x","notes":["SENTINEL-x"],"tags":[{"name":"SENTINEL-x"}]}`)), &out))
	assert.Regexp(t, hashRE, out.Title)
	assert.Equal(t, out.Title, out.Notes[0])
	assert.Equal(t, out.Title, out.Tags[0].Name)
}

func TestEnforceRevealed(t *testing.T) {
	e, _ := newTestEnforcer(t)
	in := `{"title":"SENTINEL <b>&</b>\u2028 é \"q\"","state":"SENTINEL-state","kinds":["x"],"id":1}`
	res, err := e.Enforce(Revealed, []byte(in))
	require.NoError(t, err)
	assert.Empty(t, res.Epoch, "no epoch header in revealed mode")
	assert.JSONEq(t, in, string(res.Body))

	_, err = e.Enforce(Revealed, []byte(`{"SENTINEL_key":"x"}`))
	var uk *UnknownKeyError
	require.ErrorAs(t, err, &uk, "keys are checked in revealed mode too")
}

func TestEnforceEpochMatchesHashes(t *testing.T) {
	clk := newFakeClock()
	h, err := NewHasher(time.Hour, clk.now)
	require.NoError(t, err)
	r := NewRegistry()
	require.NoError(t, r.RegisterTypes(testWork{}))
	e, err := NewEnforcer(r, h)
	require.NoError(t, err)

	first, err := e.Enforce(Hashed, []byte(`{"title":"x"}`))
	require.NoError(t, err)
	ep := h.Current()
	assert.Equal(t, ep.ID(), first.Epoch)
	assert.Equal(t, `{"title":"`+ep.Hash("x")+`"}`, string(first.Body))

	clk.advance(time.Hour)
	second, err := e.Enforce(Hashed, []byte(`{"title":"x"}`))
	require.NoError(t, err)
	assert.NotEqual(t, first.Epoch, second.Epoch)
	assert.NotEqual(t, string(first.Body), string(second.Body))
	assert.Equal(t, `{"title":"`+h.Current().Hash("x")+`"}`, string(second.Body))
}

func TestEnforceErrors(t *testing.T) {
	e, h := newTestEnforcer(t)
	cases := map[string]string{
		"unknown key":          `{"SENTINEL_key":1}`,
		"unknown nested key":   `{"items":[{"tags":[{"SENTINEL_key":"v"}]}]}`,
		"unknown key in array": `[{"id":1},{"SENTINEL_key":null}]`,
		"key case":             `{"Title":"x"}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := e.Enforce(Hashed, []byte(in))
			var uk *UnknownKeyError
			require.ErrorAs(t, err, &uk)
			assert.Regexp(t, hashRE, uk.KeyHash)
			assert.NotContains(t, err.Error(), "SENTINEL")
			assert.NotContains(t, err.Error(), "Title")
		})
	}
	assert.Equal(t, h.Current().Hash("SENTINEL_key"), func() string {
		_, err := e.Enforce(Hashed, []byte(`{"SENTINEL_key":1}`))
		var uk *UnknownKeyError
		require.ErrorAs(t, err, &uk)
		return uk.KeyHash
	}())

	invalid := map[string]string{
		"empty":          ``,
		"spaces":         `   `,
		"trailing":       `{"id":1} {"id":2}`,
		"trailing junk":  `{"id":1}SENTINEL`,
		"unclosed":       `{"title":"SENTINEL"`,
		"bare word":      `{"title":SENTINEL}`,
		"bare close":     `}`,
		"mismatched":     `[1}`,
		"non-string key": `{1:2}`,
	}
	for name, in := range invalid {
		t.Run(name, func(t *testing.T) {
			for _, mode := range []Mode{Hashed, Revealed} {
				_, err := e.Enforce(mode, []byte(in))
				require.ErrorIs(t, err, ErrInvalidJSON)
				assert.NotContains(t, err.Error(), "SENTINEL")
			}
		})
	}

	_, err := e.Enforce(Mode(7), []byte(`{}`))
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrInvalidJSON)
}

func TestModeString(t *testing.T) {
	assert.Equal(t, "hashed", Hashed.String())
	assert.Equal(t, "revealed", Revealed.String())
	assert.Equal(t, "Mode(9)", Mode(9).String())
}

// TestEnforceRealDTOMarshal runs a marshaled DTO through the enforcer, the way api.respond will.
func TestEnforceRealDTOMarshal(t *testing.T) {
	e, _ := newTestEnforcer(t)
	words := int64(1200)
	ts := time.Date(2026, 3, 4, 5, 6, 7, 800, time.UTC)
	in, err := json.Marshal(testList{Items: []testWork{{
		ID: 1, Title: "SENTINEL-title", State: "done", Words: &words, UpdatedAt: ts,
		PublishedOn: "2026-03-04", Notes: []string{"SENTINEL-note"}, Kinds: []string{"a"},
		Tags: []testTag{{ID: 2, Name: "SENTINEL-tag", Type: "character"}},
	}}, Page: 1, PerPage: 20, Total: 1})
	require.NoError(t, err)
	out := enforceHashed(t, e, string(in))
	assert.NotContains(t, out, "SENTINEL")
	var got testList
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	w := got.Items[0]
	assert.Equal(t, "done", w.State)
	assert.True(t, ts.Equal(w.UpdatedAt))
	assert.Equal(t, "2026-03-04", w.PublishedOn)
	assert.Equal(t, "character", w.Tags[0].Type)
	assert.Equal(t, []string{"a"}, w.Kinds)
	assert.Equal(t, words, *w.Words)
	assert.Regexp(t, hashRE, w.Title)
}
