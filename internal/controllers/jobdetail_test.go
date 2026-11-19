package controllers

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/jobs"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// jsonKeys lists a struct type's json keys.
func jsonKeys(t reflect.Type) []string {
	var out []string
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		out = append(out, name)
	}
	return out
}

// Every job kind has a detail type that names its kind, and every detail field can be nil, so
// a stored detail tells "not set" from a zero. No detail shares a key with JobOutcome: the two
// are merged into one stored object.
func TestJobDetailTypes(t *testing.T) {
	outcome := jsonKeys(reflect.TypeFor[JobOutcome]())
	for _, kind := range jobs.Kinds() {
		d := newJobDetail(kind)
		require.NotNil(t, d, kind)
		assert.Equal(t, kind, d.JobKind())
		typ := reflect.TypeOf(d).Elem()
		for f := range typ.Fields() {
			k := f.Type.Kind()
			assert.True(t, k == reflect.Pointer || k == reflect.Slice, "%s.%s must be a pointer or slice", typ, f.Name)
			assert.Contains(t, f.Tag.Get("json"), ",omitzero", "%s.%s", typ, f.Name)
		}
		for _, key := range jsonKeys(typ) {
			assert.NotContains(t, outcome, key, "%s key %q is also a JobOutcome key", typ, key)
		}
	}
	assert.Nil(t, newJobDetail("nope"))
}

func TestDecodeJobEvent(t *testing.T) {
	nb := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	raw, err := store.JobDetailJSON(JobOutcome{MS: new(int64(12)), Error: new("rate_limited"), NotBefore: new(nb)},
		&WorkDetail{WorkID: new(int64(7)), Chapters: new(0), Revived: new(false), MetaChanged: []string{"title"}})
	require.NoError(t, err)

	o, d, err := DecodeJobEvent(jobs.KindWork, string(raw))
	require.NoError(t, err)
	assert.Equal(t, JobOutcome{MS: new(int64(12)), Error: new("rate_limited"), NotBefore: new(nb)}, o)
	assert.Equal(t, &WorkDetail{WorkID: new(int64(7)), Chapters: new(0), Revived: new(false), MetaChanged: []string{"title"}}, d,
		"zeros survive, unset fields stay nil")

	// Events stored before the typed details decode the same.
	o, d, err = DecodeJobEvent(jobs.KindSourceAdd, `{"blurbs":20,"error":"x:y","ms":5,"not_before":"2026-01-02T03:04:05Z",`+
		`"estimate":{"Total":40,"Duration":410000000000},"source_id":1,"state":"active"}`)
	require.NoError(t, err)
	assert.Equal(t, nb, *o.NotBefore)
	assert.Equal(t, &SourceAddDetail{SourceID: new(int64(1)), State: new("active"), Blurbs: new(20),
		Estimate: &Estimate{Total: 40, Duration: 410 * time.Second}}, d)

	o, d, err = DecodeJobEvent("nope", `{"attempt":1}`)
	require.NoError(t, err)
	assert.Equal(t, new(1), o.Attempt)
	assert.Nil(t, d, "unknown kind: outcome only")

	_, _, err = DecodeJobEvent(jobs.KindPoll, `{"page":"x"}`)
	require.Error(t, err)
}
