package redact

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The fuzz checks use their own decoding (encoding/json into any) and their own allow rules,
// independent of the enforcer's token walker.

var (
	fuzzEnums = map[string][]string{
		"state": {"queued", "running", "done"},
		"type":  {"fandom", "character"},
		"kinds": {"a", "b"},
	}
	fuzzTimestampRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d*[1-9])?Z$`)
	fuzzDateRE      = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

func fuzzAllowed(key, s string) bool {
	if vals, ok := fuzzEnums[key]; ok {
		return slices.Contains(vals, s)
	}
	switch {
	case strings.HasSuffix(key, "_at"):
		_, err := time.Parse(time.RFC3339, s)
		return fuzzTimestampRE.MatchString(s) && err == nil
	case strings.HasSuffix(key, "_on"):
		_, err := time.Parse(time.DateOnly, s)
		return fuzzDateRE.MatchString(s) && err == nil
	}
	return false
}

func decodeAny(t *testing.T, b []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	require.NoError(t, dec.Decode(&v))
	return v
}

// checkHashed asserts out is in with every string either allowed for its key (unchanged) or
// replaced by the epoch's hash; numbers, booleans, null and structure unchanged.
func checkHashed(t *testing.T, ep *Epoch, key string, in, out any) {
	t.Helper()
	switch iv := in.(type) {
	case map[string]any:
		ov, ok := out.(map[string]any)
		require.True(t, ok, "object became %T", out)
		require.Len(t, ov, len(iv))
		for k, v := range iv {
			o, ok := ov[k]
			require.True(t, ok, "key %q missing", k)
			checkHashed(t, ep, k, v, o)
		}
	case []any:
		ov, ok := out.([]any)
		require.True(t, ok, "array became %T", out)
		require.Len(t, ov, len(iv))
		for i := range iv {
			checkHashed(t, ep, key, iv[i], ov[i])
		}
	case string:
		os, ok := out.(string)
		require.True(t, ok, "string became %T", out)
		if fuzzAllowed(key, iv) {
			require.Equal(t, iv, os)
		} else {
			require.Regexp(t, hashRE, os)
			require.Equal(t, ep.Hash(iv), os)
		}
	default: // json.Number, bool, nil
		require.Equal(t, in, out)
	}
}

// fuzzGen builds a random JSON tree over the registered keys from fuzz bytes.
type fuzzGen struct {
	b    []byte
	keys []string
	strs []string
}

func (g *fuzzGen) next() int {
	if len(g.b) == 0 {
		return 0
	}
	c := g.b[0]
	g.b = g.b[1:]
	return int(c)
}

func (g *fuzzGen) value(depth int) any {
	c := g.next()
	if depth >= 5 {
		c = 0
	}
	switch c % 7 {
	case 0, 1:
		return g.strs[g.next()%len(g.strs)]
	case 2:
		return json.Number(fmt.Sprint(c - 100))
	case 3:
		return c%2 == 0
	case 4:
		return nil
	case 5:
		m := map[string]any{}
		for range g.next() % 5 {
			m[g.keys[g.next()%len(g.keys)]] = g.value(depth + 1)
		}
		return m
	default:
		a := []any{}
		for range g.next() % 5 {
			a = append(a, g.value(depth+1))
		}
		return a
	}
}

func FuzzEnforceStructured(f *testing.F) {
	f.Add([]byte{5, 3, 0, 0, 1}, "x")
	f.Add([]byte{5, 4, 1, 0, 2, 1, 6, 3, 0, 0, 0, 1, 5, 2}, "2026-01-02T03:04:05Z")
	f.Add([]byte{6, 4, 0, 0, 1, 0, 2, 5, 1, 7, 0, 3}, "")
	e, h := newTestEnforcer(f)
	keys := keysOf(&Registry{keys: e.keys})
	f.Fuzz(func(t *testing.T, data []byte, s string) {
		strs := []string{
			"SENTINEL-1", "SENTINEL-" + s, s + "SENTINEL", "SENTINEL",
			"2026-01-02T03:04:05Z", "2026-01-02", "2026-01-02T03:04:05.120Z", s,
		}
		for _, vals := range fuzzEnums {
			strs = append(strs, vals...)
		}
		g := &fuzzGen{b: data, keys: keys, strs: strs}
		tree := map[string]any{"items": []any{g.value(0), g.value(0)}, "title": "SENTINEL-root"}
		in, err := json.Marshal(tree)
		require.NoError(t, err)

		res, err := e.Enforce(Hashed, in)
		require.NoError(t, err)
		require.NotContains(t, string(res.Body), "SENTINEL")
		checkHashed(t, h.Current(), "", decodeAny(t, in), decodeAny(t, res.Body))

		rev, err := e.Enforce(Revealed, in)
		require.NoError(t, err)
		require.Equal(t, decodeAny(t, in), decodeAny(t, rev.Body))
	})
}

func FuzzEnforceRawJSON(f *testing.F) {
	for _, s := range []string{
		`{"title":"x","state":"done","updated_at":"2026-01-02T03:04:05Z"}`,
		`[{"items":[{"tags":[{"name":"a","type":"fandom"}]}]},1,null,true,"s"]`,
		`{"kinds":["a",["b",{"id":1e400}]],"published_on":"2026-02-29"}`,
		`{"title":"\ud800\u2028<>&","notes":["\x00"]}`,
		`{"unknown":1}`, `{"id":1}{}`, `"`, ``,
	} {
		f.Add([]byte(s))
	}
	e, h := newTestEnforcer(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		res, err := e.Enforce(Hashed, data)
		var uk *UnknownKeyError
		switch {
		case err == nil:
			require.True(t, json.Valid(data))
			require.True(t, json.Valid(res.Body))
			checkHashed(t, h.Current(), "", decodeAny(t, data), decodeAny(t, res.Body))
		case errors.As(err, &uk):
			// The walk stops at the first unknown key, so later syntax errors go unreported.
			require.Regexp(t, hashRE, uk.KeyHash)
		case errors.Is(err, ErrInvalidJSON):
			require.False(t, json.Valid(data), "valid JSON rejected: %v", err)
		default:
			t.Fatalf("unexpected error %v", err)
		}

		rev, rerr := e.Enforce(Revealed, data)
		require.Equal(t, err == nil, rerr == nil)
		if rerr == nil {
			require.Equal(t, decodeAny(t, data), decodeAny(t, rev.Body))
		}
	})
}

// checkTyped is checkHashed for EnforceAs, deciding what may pass from the Go type typ by
// reflection (not from the compiled schema): an Enum field's own values, the _at/_on rules for
// other strings and time.Time, and nothing else.
func checkTyped(t *testing.T, ep *Epoch, typ reflect.Type, key string, in, out any) {
	t.Helper()
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if in == nil {
		require.Nil(t, out)
		return
	}
	switch iv := in.(type) {
	case map[string]any:
		require.Equal(t, reflect.Struct, typ.Kind(), "object accepted for %s", typ)
		ov, ok := out.(map[string]any)
		require.True(t, ok, "object became %T", out)
		require.Len(t, ov, len(iv))
		for k, v := range iv {
			f, ok := fieldByKey(typ, k)
			require.True(t, ok, "key %q accepted for %s", k, typ)
			checkTyped(t, ep, f, k, v, ov[k])
		}
	case []any:
		require.Contains(t, []reflect.Kind{reflect.Slice, reflect.Array}, typ.Kind(), "array accepted for %s", typ)
		ov, ok := out.([]any)
		require.True(t, ok, "array became %T", out)
		require.Len(t, ov, len(iv))
		for i := range iv {
			checkTyped(t, ep, typ.Elem(), key, iv[i], ov[i])
		}
	case string:
		var allowed bool
		switch {
		case implements(typ, enumType):
			allowed = slices.Contains(reflect.New(typ).Interface().(Enum).EnumValues(), iv)
		case typ == reflect.TypeFor[time.Time]() || typ.Kind() == reflect.String:
			allowed = fuzzAllowed(key, iv)
		default:
			t.Fatalf("string accepted for %s", typ)
		}
		os, ok := out.(string)
		require.True(t, ok, "string became %T", out)
		if allowed {
			require.Equal(t, iv, os)
		} else {
			require.Equal(t, ep.Hash(iv), os)
		}
	default: // json.Number, bool
		require.Equal(t, in, out)
	}
}

// fieldByKey finds the field of struct t with json key k (the fuzz types embed nothing).
func fieldByKey(t reflect.Type, k string) (reflect.Type, bool) {
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == k {
			return f.Type, true
		}
	}
	return nil, false
}

func FuzzEnforceAsRawJSON(f *testing.F) {
	for _, s := range []string{
		`{"status":"ok","version":"v1.2.3","works":[{"id":1,"status":"fetched","title":"x","seen":["pending","ok"]}]}`,
		`{"status":"fetched","images":[{"status":"ok","count":2,"stored_at":"2026-01-02T03:04:05Z"},null]}`,
		`{"matrix":[["a"],[]],"sampled_at":["2026-01-02T03:04:05.5Z"],"day_on":"2026-02-29","paused":false}`,
		`{"next":{"status":"ok","next":{"works":[{"status":"stored"}]}}}`,
		`{"works":[{"stored_at":"x"}]}`, `{"next":[]}`, `{"status":1}`, `[]`, `"x"`, ``,
	} {
		f.Add([]byte(s))
	}
	e, h := newTypedEnforcer(f)
	typ := reflect.TypeFor[tStatus]()
	f.Fuzz(func(t *testing.T, body []byte) {
		res, err := e.EnforceAs(Hashed, typ, body)
		switch {
		case err == nil:
			require.True(t, json.Valid(body))
			require.True(t, json.Valid(res.Body))
			checkTyped(t, h.Current(), typ, "", decodeAny(t, body), decodeAny(t, res.Body))
		case isErr[*UnknownKeyError](err):
			// The walk stops at the first unknown key or mismatch, so later syntax errors go
			// unreported.
		case isErr[*SchemaError](err):
		case errors.Is(err, ErrInvalidJSON):
			require.False(t, json.Valid(body), "valid JSON rejected: %v", err)
		default:
			t.Fatalf("unexpected error %v", err)
		}

		rev, rerr := e.EnforceAs(Revealed, typ, body)
		require.Equal(t, err == nil, rerr == nil)
		if rerr == nil {
			require.Equal(t, decodeAny(t, body), decodeAny(t, rev.Body))
		}
	})
}

func isErr[E error](err error) bool {
	_, ok := errors.AsType[E](err)
	return ok
}
