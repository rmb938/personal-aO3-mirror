package redact

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Synthetic DTOs for EnforceAs: two `status` enums with disjoint values, and a `kind` enum
// shared by name with a plain field.
type healthStat string

func (healthStat) EnumValues() []string { return []string{"ok"} }

type workStat string

func (workStat) EnumValues() []string { return []string{"fetched", "pending"} }

type imageStat string

func (imageStat) EnumValues() []string { return []string{"stored", "failed"} }

type tWork struct {
	ID     int64      `json:"id"`
	Status workStat   `json:"status"`
	Title  string     `json:"title"`
	Seen   []workStat `json:"seen,omitzero"`
}

type tImage struct {
	Status   imageStat `json:"status"`
	Count    int       `json:"count"`
	StoredAt time.Time `json:"stored_at,omitzero"`
}

type tStatus struct {
	Health    healthStat  `json:"status"`
	Version   string      `json:"version"`
	Works     []tWork     `json:"works"`
	Images    []*tImage   `json:"images"`
	Matrix    [][]string  `json:"matrix"`
	SampledAt []time.Time `json:"sampled_at"`
	DayOn     string      `json:"day_on"`
	Paused    bool        `json:"paused"`
	Next      *tStatus    `json:"next"`
}

type tVersion struct {
	Version string `json:"version"`
}

func newTypedEnforcer(tb testing.TB) (*Enforcer, *Hasher) {
	tb.Helper()
	h, err := NewHasher(0, nil)
	require.NoError(tb, err)
	r := NewRegistry()
	require.NoError(tb, r.RegisterTypes(tStatus{}, tVersion{}))
	require.NoError(tb, r.RegisterFieldEnum(tVersion{}, "version", "v1.2.3"))
	e, err := NewEnforcer(r, h)
	require.NoError(tb, err)
	return e, h
}

func enforceAs[T any](t *testing.T, e *Enforcer, mode Mode, in string) (Result, error) {
	t.Helper()
	return e.EnforceAs(mode, reflect.TypeFor[T](), []byte(in))
}

func TestEnforceAsEnumsPerField(t *testing.T) {
	e, h := newTypedEnforcer(t)
	hs := h.Current().Hash
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }

	cases := []struct {
		name, in, want string
	}{
		{"own values pass", `{"status":"ok","works":[{"status":"fetched"}],"images":[{"status":"stored"}]}`,
			`{"status":"ok","works":[{"status":"fetched"}],"images":[{"status":"stored"}]}`},
		{"work value under health hashed", `{"status":"fetched"}`, `{"status":` + q(hs("fetched")) + `}`},
		{"image value under work hashed", `{"works":[{"status":"stored"}]}`, `{"works":[{"status":` + q(hs("stored")) + `}]}`},
		{"health value under image hashed", `{"images":[{"status":"ok"}]}`, `{"images":[{"status":` + q(hs("ok")) + `}]}`},
		{"enum slice", `{"works":[{"seen":["pending","ok"]}]}`, `{"works":[{"seen":["pending",` + q(hs("ok")) + `]}]}`},
		{"recursive type", `{"next":{"status":"ok","next":{"works":[{"status":"pending"}]}}}`,
			`{"next":{"status":"ok","next":{"works":[{"status":"pending"}]}}}`},
		{"free text hashed", `{"works":[{"title":"SENTINEL-t"}]}`, `{"works":[{"title":` + q(hs("SENTINEL-t")) + `}]}`},
		{"field enum not on other types", `{"version":"v1.2.3"}`, `{"version":` + q(hs("v1.2.3")) + `}`},
		{"timestamp in opaque", `{"images":[{"stored_at":"2026-01-02T03:04:05Z"}]}`, `{"images":[{"stored_at":"2026-01-02T03:04:05Z"}]}`},
		{"timestamp slice", `{"sampled_at":["2026-01-02T03:04:05Z","x"]}`, `{"sampled_at":["2026-01-02T03:04:05Z",` + q(hs("x")) + `]}`},
		{"date", `{"day_on":"2026-02-28"}`, `{"day_on":"2026-02-28"}`},
		{"nested arrays", `{"matrix":[["x"],[]]}`, `{"matrix":[[` + q(hs("x")) + `],[]]}`},
		{"null anywhere", `{"status":null,"works":null,"images":[null],"paused":null,"next":null}`,
			`{"status":null,"works":null,"images":[null],"paused":null,"next":null}`},
		{"scalars", `{"paused":true,"images":[{"count":-3}]}`, `{"paused":true,"images":[{"count":-3}]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := enforceAs[tStatus](t, e, Hashed, c.in)
			require.NoError(t, err)
			assert.Equal(t, c.want, string(res.Body))
		})
	}

	res, err := enforceAs[tVersion](t, e, Hashed, `{"version":"v1.2.3"}`)
	require.NoError(t, err)
	assert.JSONEq(t, `{"version":"v1.2.3"}`, string(res.Body), "field enum on its own type")
	res, err = enforceAs[*tVersion](t, e, Hashed, `{"version":"v9"}`)
	require.NoError(t, err, "a pointer to a registered type is accepted")
	assert.NotContains(t, string(res.Body), "v9")
	assert.Equal(t, []string{"redact.tVersion.Version"}, res.OffEnum)
}

func TestEnforceAsOffEnumPaths(t *testing.T) {
	e, _ := newTypedEnforcer(t)
	in := `{"status":"fetched","works":[{"status":"ok"},{"status":"stored"}],"images":[{"status":"stored"}]}`
	for _, mode := range []Mode{Hashed, Revealed} {
		res, err := enforceAs[tStatus](t, e, mode, in)
		require.NoError(t, err)
		assert.Equal(t, []string{"redact.tStatus.Health", "redact.tWork.Status"}, res.OffEnum, mode.String())
	}
}

func TestEnforceAsKeysPerStruct(t *testing.T) {
	e, _ := newTypedEnforcer(t)
	for name, in := range map[string]string{
		"other struct's key at top": `{"count":1}`,
		"other struct's key nested": `{"works":[{"stored_at":"2026-01-02T03:04:05Z"}]}`,
		"parent key in child":       `{"images":[{"paused":true}]}`,
		"unregistered key":          `{"SENTINEL_key":1}`,
		"parent key deeper down":    `{"version":"x","next":{"works":[{"id":1,"version":"v"}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			for _, mode := range []Mode{Hashed, Revealed} {
				_, err := enforceAs[tStatus](t, e, mode, in)
				var uk *UnknownKeyError
				require.ErrorAs(t, err, &uk)
				assert.NotContains(t, err.Error(), "SENTINEL")
			}
		})
	}
}

func TestEnforceAsSchemaMismatch(t *testing.T) {
	e, _ := newTypedEnforcer(t)
	for name, c := range map[string]struct{ in, path string }{
		"object for enum":      {`{"status":{}}`, "redact.tStatus.Health"},
		"number for string":    {`{"works":[{"title":1}]}`, "redact.tWork.Title"},
		"string for number":    {`{"works":[{"id":"SENTINEL"}]}`, "redact.tWork.ID"},
		"bool for number":      {`{"images":[{"count":true}]}`, "redact.tImage.Count"},
		"array for object":     {`{"next":[]}`, "redact.tStatus"}, // struct nodes name their type
		"object for array":     {`{"works":{}}`, "redact.tStatus.Works"},
		"string for array el":  {`{"works":["SENTINEL"]}`, "redact.tWork"},
		"object in opaque":     {`{"images":[{"stored_at":{}}]}`, "redact.tImage.StoredAt"},
		"array at top":         {`[]`, "redact.tStatus"},
		"string at top":        {`"SENTINEL"`, "redact.tStatus"},
		"too deep nested list": {`{"matrix":[[["x"]]]}`, "redact.tStatus.Matrix"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := enforceAs[tStatus](t, e, Hashed, c.in)
			var se *SchemaError
			require.ErrorAs(t, err, &se)
			assert.Equal(t, c.path, se.Path)
			assert.NotContains(t, err.Error(), "SENTINEL")
		})
	}

	_, err := enforceAs[tStatus](t, e, Hashed, `{"status":"ok"`)
	require.ErrorIs(t, err, ErrInvalidJSON)
	_, err = enforceAs[tStatus](t, e, Mode(7), `{}`)
	require.Error(t, err)
	_, err = enforceAs[tWork](t, e, Hashed, `{}`)
	require.ErrorContains(t, err, "not registered", "only registered roots, not nested types")
}

func TestEnforceAsRevealed(t *testing.T) {
	e, _ := newTypedEnforcer(t)
	in := `{"status":"SENTINEL-s","works":[{"title":"SENTINEL <b>&</b>","status":"x"}]}`
	res, err := enforceAs[tStatus](t, e, Revealed, in)
	require.NoError(t, err)
	assert.JSONEq(t, in, string(res.Body))
	assert.Empty(t, res.Epoch)
}

// TestEnforceAsRealMarshal runs a json/v2-marshaled value through EnforceAs, as api.respond does.
func TestEnforceAsRealMarshal(t *testing.T) {
	e, _ := newTypedEnforcer(t)
	ts := time.Date(2026, 3, 4, 5, 6, 7, 800, time.UTC)
	v := tStatus{
		Health: "ok", Version: "SENTINEL-v",
		Works:  []tWork{{ID: 1, Status: "pending", Title: "SENTINEL-title", Seen: []workStat{"fetched"}}},
		Images: []*tImage{{Status: "failed", Count: 2, StoredAt: ts}, nil},
		Next:   &tStatus{Health: "ok"},
	}
	in, err := jsonv2.Marshal(v)
	require.NoError(t, err)
	res, err := e.EnforceAs(Hashed, reflect.TypeOf(&v), in)
	require.NoError(t, err)
	assert.NotContains(t, string(res.Body), "SENTINEL")
	assert.Empty(t, res.OffEnum)
	var got tStatus
	require.NoError(t, jsonv2.Unmarshal(res.Body, &got))
	assert.Equal(t, healthStat("ok"), got.Health)
	assert.Equal(t, workStat("pending"), got.Works[0].Status)
	assert.Equal(t, imageStat("failed"), got.Images[0].Status)
	assert.True(t, ts.Equal(got.Images[0].StoredAt))
	assert.Regexp(t, hashRE, got.Works[0].Title)
}

type tEmbedBase struct {
	Label string `json:"label"`
}

type tEmbedder struct {
	tEmbedBase
	ID int `json:"id"`
}

func TestRegisterFieldEnum(t *testing.T) {
	h, err := NewHasher(0, nil)
	require.NoError(t, err)

	t.Run("promoted field keyed by declaring type", func(t *testing.T) {
		r := NewRegistry()
		require.NoError(t, r.RegisterTypes(tEmbedder{}))
		require.NoError(t, r.RegisterFieldEnum(tEmbedBase{}, "label", "a"))
		require.NoError(t, r.RegisterFieldEnum(&tEmbedBase{}, "label", "b"), "adding values; pointer accepted")
		e, err := NewEnforcer(r, h)
		require.NoError(t, err)
		res, err := enforceAs[tEmbedder](t, e, Hashed, `{"label":"b","id":1}`)
		require.NoError(t, err)
		assert.JSONEq(t, `{"label":"b","id":1}`, string(res.Body))
	})
	t.Run("snapshot", func(t *testing.T) {
		r := NewRegistry()
		require.NoError(t, r.RegisterTypes(tVersion{}))
		require.NoError(t, r.RegisterFieldEnum(tVersion{}, "version", "a"))
		e, err := NewEnforcer(r, h)
		require.NoError(t, err)
		require.NoError(t, r.RegisterFieldEnum(tVersion{}, "version", "later"))
		res, err := enforceAs[tVersion](t, e, Hashed, `{"version":"later"}`)
		require.NoError(t, err)
		assert.NotContains(t, string(res.Body), "later")
	})

	r := NewRegistry()
	require.Error(t, r.RegisterFieldEnum("x", "version", "a"), "not a struct")
	require.Error(t, r.RegisterFieldEnum(nil, "version", "a"))
	require.Error(t, r.RegisterFieldEnum(tVersion{}, "Version", "a"))
	require.Error(t, r.RegisterFieldEnum(tVersion{}, "version"))
	require.Error(t, r.RegisterFieldEnum(tVersion{}, "version", ""))

	for name, c := range map[string]struct {
		v   any
		key string
	}{
		"missing field":      {tVersion{}, "nope"},
		"type not reachable": {tEmbedBase{}, "label"},
		"enum-typed field":   {tWork{}, "status"},
		"number field":       {tWork{}, "id"},
		"opaque field":       {tImage{}, "stored_at"},
		"struct field":       {tStatus{}, "next"},
	} {
		t.Run(name, func(t *testing.T) {
			r := NewRegistry()
			require.NoError(t, r.RegisterTypes(tStatus{}, tVersion{}))
			require.NoError(t, r.RegisterFieldEnum(c.v, c.key, "a"))
			_, err := NewEnforcer(r, h)
			assert.Error(t, err)
		})
	}
}

func TestNewEnforcerRejectsSchemas(t *testing.T) {
	h, err := NewHasher(0, nil)
	require.NoError(t, err)
	type withBytes struct {
		B []byte `json:"b"`
	}
	type withByteArray struct {
		B [4]byte `json:"b"`
	}
	type withStringOpt struct {
		N int `json:"n,string"`
	}
	type withFormat struct {
		T time.Time `json:"t,format:RFC1123"`
	}
	type dupInner struct {
		ID int `json:"id"`
	}
	type withDup struct {
		dupInner
		ID int `json:"id"`
	}
	for name, v := range map[string]any{
		"byte slice": withBytes{}, "byte array": withByteArray{}, "string option": withStringOpt{},
		"format option": withFormat{}, "duplicate key via embedding": withDup{},
	} {
		t.Run(name, func(t *testing.T) {
			r := NewRegistry()
			require.NoError(t, r.RegisterTypes(v), "the key walk accepts it")
			_, err := NewEnforcer(r, h)
			assert.Error(t, err)
		})
	}
}
