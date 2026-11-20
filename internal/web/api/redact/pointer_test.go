package redact

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ptrFilter struct {
	TagID int64    `json:"tag_id"`
	Names []string `json:"names"`
}

type ptrRequest struct {
	URL     string      `json:"url"`
	Filters []ptrFilter `json:"filters"`
	Nested  *ptrFilter  `json:"nested"`
	Grid    [][]int     `json:"grid"`
}

type ptrOther struct {
	Query string `json:"query"`
}

type ptrItem struct {
	Detail  string `json:"detail"`
	Pointer string `json:"pointer"`
}

type ptrProblem struct {
	Errors []ptrItem `json:"errors"`
}

func ptrEnforcer(t *testing.T, requestTypes ...any) *Enforcer {
	t.Helper()
	reg := NewRegistry()
	require.NoError(t, reg.RegisterTypes(ptrProblem{}))
	require.NoError(t, reg.RegisterPointerField(ptrItem{}, "pointer", requestTypes...))
	h, err := NewHasher(0, nil)
	require.NoError(t, err)
	e, err := NewEnforcer(reg, h)
	require.NoError(t, err)
	return e
}

// enforcePointer runs one pointer through the enforcer and reports whether it passed unhashed.
func enforcePointer(t *testing.T, e *Enforcer, p string) (passed bool, off []string) {
	t.Helper()
	body, err := json.Marshal(ptrProblem{Errors: []ptrItem{{Detail: "d", Pointer: p}}})
	require.NoError(t, err)
	res, err := e.Enforce(Hashed, reflect.TypeFor[ptrProblem](), body)
	require.NoError(t, err)
	var out ptrProblem
	require.NoError(t, json.Unmarshal(res.Body, &out))
	return out.Errors[0].Pointer == p, res.OffEnum
}

func TestPointerField(t *testing.T) {
	e := ptrEnforcer(t, ptrRequest{}, ptrOther{})
	for _, p := range []string{"/url", "/filters", "/filters/0", "/filters/12/tag_id", "/filters/3/names/7",
		"/nested", "/nested/tag_id", "/grid/1/2", "/query"} {
		passed, off := enforcePointer(t, e, p)
		assert.True(t, passed, p)
		assert.Empty(t, off, p)
	}
	for _, p := range []string{"", "/", "url", "/URL", "/nope", "/url/x", "/filters/x", "/filters/01",
		"/filters/-1", "/filters/1000000", "/filters/0/nope", "/filters//tag_id", "/url/", "/nested/names/0/x",
		"/grid/1/2/3", "/fil~0ters", "/SENTINEL-x", "#/url"} {
		passed, off := enforcePointer(t, e, p)
		assert.False(t, passed, "%q is hashed", p)
		assert.Equal(t, []string{"redact.ptrItem.Pointer"}, off, "%q is reported", p)
	}
}

func TestPointerFieldNoRequestTypes(t *testing.T) {
	e := ptrEnforcer(t)
	passed, _ := enforcePointer(t, e, "/url")
	assert.False(t, passed, "with no request types every pointer is hashed")
}

func TestPointerFieldRevealed(t *testing.T) {
	e := ptrEnforcer(t, ptrRequest{})
	body := []byte(`{"errors":[{"detail":"d","pointer":"/anything"}]}`)
	res, err := e.Enforce(Revealed, reflect.TypeFor[ptrProblem](), body)
	require.NoError(t, err)
	assert.Contains(t, string(res.Body), "/anything")
}

func TestRegisterPointerFieldRejects(t *testing.T) {
	type notString struct {
		Pointer int `json:"pointer"`
	}
	type badRequest struct {
		M map[string]string `json:"m"`
	}
	cases := map[string]func(*Registry) error{
		"not a struct": func(r *Registry) error { return r.RegisterPointerField("x", "pointer") },
		"request not struct": func(r *Registry) error {
			return r.RegisterPointerField(ptrItem{}, "pointer", 1)
		},
		"bad request type": func(r *Registry) error {
			return r.RegisterPointerField(ptrItem{}, "pointer", badRequest{})
		},
		"twice": func(r *Registry) error {
			if err := r.RegisterPointerField(ptrItem{}, "pointer"); err != nil {
				return err
			}
			return r.RegisterPointerField(ptrItem{}, "pointer")
		},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) { assert.Error(t, f(NewRegistry())) })
	}

	h, err := NewHasher(0, nil)
	require.NoError(t, err)
	enforcerErr := func(register func(*Registry) error) error {
		reg := NewRegistry()
		require.NoError(t, reg.RegisterTypes(ptrProblem{}, notString{}))
		require.NoError(t, register(reg))
		_, err := NewEnforcer(reg, h)
		return err
	}
	err = enforcerErr(func(r *Registry) error { return r.RegisterPointerField(ptrItem{}, "nope", ptrRequest{}) })
	require.ErrorContains(t, err, "no such field")
	err = enforcerErr(func(r *Registry) error { return r.RegisterPointerField(notString{}, "pointer", ptrRequest{}) })
	require.ErrorContains(t, err, "not a plain string")
	err = enforcerErr(func(r *Registry) error {
		if err := r.RegisterFieldEnum(ptrItem{}, "pointer", "/url"); err != nil {
			return err
		}
		return r.RegisterPointerField(ptrItem{}, "pointer", ptrRequest{})
	})
	assert.ErrorContains(t, err, "both")
}

func FuzzPointerIn(f *testing.F) {
	for _, s := range []string{"/url", "/filters/3/names/0", "/grid/0/0", "", "/", "//", "/filters/01"} {
		f.Add(s)
	}
	c := newCompiler(nil, nil)
	root, err := c.compileStruct(reflect.TypeFor[ptrRequest]())
	if err != nil {
		f.Fatal(err)
	}
	keys := map[string]bool{"url": true, "filters": true, "nested": true, "grid": true, "tag_id": true, "names": true}
	f.Fuzz(func(t *testing.T, s string) {
		if !pointerIn(s, []*node{root}) {
			return
		}
		// Oracle: an allowed pointer is "/" + segments that are each a known key or an index.
		for seg := range strings.SplitSeq(s[1:], "/") {
			if !keys[seg] && !isIndex(seg) {
				t.Fatalf("%q passed with segment %q", s, seg)
			}
		}
	})
}
