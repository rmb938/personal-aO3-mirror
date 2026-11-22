package redact

import (
	"bytes"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The fuzz checks use their own decoding (encoding/json into any) and decide what may pass
// from the Go type by reflection, independent of the enforcer's compiled schema and walker.

var (
	fuzzTimestampRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d*[1-9])?Z$`)
	fuzzDateRE      = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	// fuzzTokenRE is tToken's pattern, written out again: lower case only, so no sentinel
	// ("SENTINEL") can ever match it.
	fuzzTokenRE = regexp.MustCompile(`\Atok_[a-z0-9]{8}\z`)
)

// verbatimMismatch reports whether some tToken string in the decoded JSON v (at typ) does not
// match fuzzTokenRE: the enforcer must then refuse the body with a *PatternError.
func verbatimMismatch(typ reflect.Type, v any) bool {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch iv := v.(type) {
	case map[string]any:
		if typ.Kind() != reflect.Struct {
			return false // a shape mismatch (raw JSON fuzzing), not a token
		}
		for k, child := range iv {
			if f, ok := fieldByKey(typ, k); ok && verbatimMismatch(f, child) {
				return true
			}
		}
	case []any:
		if typ.Kind() != reflect.Slice && typ.Kind() != reflect.Array {
			return false
		}
		for _, child := range iv {
			if verbatimMismatch(typ.Elem(), child) {
				return true
			}
		}
	case string:
		return typ == reflect.TypeFor[tToken]() && !fuzzTokenRE.MatchString(iv)
	}
	return false
}

// fuzzSuffixAllowed is the _at/_on rule for a non-enum string field.
func fuzzSuffixAllowed(key, s string) bool {
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

// checkTyped asserts out is in, as typ, with every string either allowed for its field
// (unchanged) or replaced by the epoch's hash: an Enum field's own values, the _at/_on rules
// for other strings and time.Time, and nothing else. Numbers, booleans, null and structure are
// unchanged, and every object and array sits where typ has a struct or a slice.
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
		case typ == reflect.TypeFor[tToken]():
			require.Regexp(t, fuzzTokenRE, iv, "a Verbatim value passed without matching")
			allowed = true
		case implements(typ, enumType):
			allowed = slices.Contains(reflect.New(typ).Interface().(Enum).EnumValues(), iv)
		case typ == reflect.TypeFor[time.Time]() || typ.Kind() == reflect.String:
			allowed = fuzzSuffixAllowed(key, iv)
		default:
			t.Fatalf("string accepted for %s", typ)
		}
		os, ok := out.(string)
		require.True(t, ok, "string became %T", out)
		if allowed {
			require.Equal(t, iv, os)
		} else {
			require.Regexp(t, hashRE, os)
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

// fuzzGen fills a Go value from fuzz bytes.
type fuzzGen struct {
	b     []byte
	strs  []string
	times []time.Time
}

func (g *fuzzGen) next() int {
	if len(g.b) == 0 {
		return 0
	}
	c := g.b[0]
	g.b = g.b[1:]
	return int(c)
}

func (g *fuzzGen) fill(v reflect.Value, depth int) {
	switch v.Kind() {
	case reflect.Pointer:
		if depth >= 3 || g.next()%3 == 0 {
			return // nil
		}
		v.Set(reflect.New(v.Type().Elem()))
		g.fill(v.Elem(), depth+1)
	case reflect.Slice:
		n := g.next() % 4
		if depth >= 3 {
			n = 0
		}
		s := reflect.MakeSlice(v.Type(), n, n)
		for i := range n {
			g.fill(s.Index(i), depth+1)
		}
		v.Set(s)
	case reflect.Struct:
		if v.Type() == reflect.TypeFor[time.Time]() {
			v.Set(reflect.ValueOf(g.times[g.next()%len(g.times)]))
			return
		}
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				g.fill(v.Field(i), depth)
			}
		}
	case reflect.String:
		v.SetString(g.strs[g.next()%len(g.strs)])
	case reflect.Int, reflect.Int64:
		v.SetInt(int64(g.next() - 100))
	case reflect.Bool:
		v.SetBool(g.next()%2 == 0)
	default:
		panic("fuzzGen: unhandled kind " + v.Kind().String())
	}
}

// FuzzEnforceStructured marshals random tStatus values (as api.respond does) and checks that
// no sentinel survives hashing and that each string passes only where its own field allows it.
func FuzzEnforceStructured(f *testing.F) {
	f.Add([]byte{5, 3, 0, 0, 1}, "x")
	f.Add([]byte{1, 4, 1, 0, 2, 1, 6, 3, 0, 0, 0, 1, 5, 2, 9, 9, 9}, "2026-01-02T03:04:05Z")
	f.Add([]byte{6, 4, 0, 0, 1, 0, 2, 5, 1, 7, 0, 3}, "")
	e, h := newTypedEnforcer(f)
	typ := reflect.TypeFor[tStatus]()
	var enumVals []string
	for _, en := range []Enum{healthStat(""), workStat(""), imageStat("")} {
		enumVals = append(enumVals, en.EnumValues()...)
	}
	times := []time.Time{
		time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), time.Date(2026, 1, 2, 3, 4, 5, 120e6, time.UTC),
		time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600)), {},
	}
	f.Fuzz(func(t *testing.T, b []byte, s string) {
		s = strings.ToValidUTF8(s, "�") // json/v2 refuses to marshal invalid UTF-8
		strs := append([]string{
			"SENTINEL-1", "SENTINEL-" + s, s + "SENTINEL", "SENTINEL", "v1.2.3",
			"2026-01-02T03:04:05Z", "2026-01-02", "2026-01-02T03:04:05.120Z", s,
			"tok_abcd1234", "tok_" + s, "tok_SENTINEL",
		}, enumVals...)
		var v tStatus
		(&fuzzGen{b: b, strs: strs, times: times}).fill(reflect.ValueOf(&v).Elem(), 0)
		in, err := jsonv2.Marshal(v)
		require.NoError(t, err)
		mismatch := verbatimMismatch(typ, decodeAny(t, in))

		res, err := e.Enforce(Hashed, typ, in)
		rev, rerr := e.Enforce(Revealed, typ, in)
		if mismatch {
			require.True(t, isErr[*PatternError](err), "hashed: want *PatternError, got %v", err)
			require.True(t, isErr[*PatternError](rerr), "revealed: want *PatternError, got %v", rerr)
			return
		}
		require.NoError(t, err)
		require.NotContains(t, string(res.Body), "SENTINEL")
		checkTyped(t, h.Current(), typ, "", decodeAny(t, in), decodeAny(t, res.Body))

		require.NoError(t, rerr)
		require.Equal(t, decodeAny(t, in), decodeAny(t, rev.Body))
	})
}

func FuzzEnforceRawJSON(f *testing.F) {
	for _, s := range []string{
		`{"status":"ok","version":"v1.2.3","works":[{"id":1,"status":"fetched","title":"x","seen":["pending","ok"]}]}`,
		`{"status":"fetched","images":[{"status":"ok","count":2,"stored_at":"2026-01-02T03:04:05Z"},null]}`,
		`{"matrix":[["a"],[]],"sampled_at":["2026-01-02T03:04:05.5Z"],"day_on":"2026-02-29","paused":false}`,
		`{"next":{"status":"ok","next":{"works":[{"status":"stored"}]}}}`,
		`{"works":[{"title":"\ud800 <>&","id":1e400}]}`,
		`{"works":[{"token":"tok_abcd1234"},{"token":"tok_SENTINEL"}]}`, `{"works":[{"token":"tok_0000000a"}]}`,
		`{"works":[{"stored_at":"x"}]}`, `{"next":[]}`, `{"status":1}`, `{"id":1}{}`, `[]`, `"x"`, `"`, ``,
	} {
		f.Add([]byte(s))
	}
	e, h := newTypedEnforcer(f)
	typ := reflect.TypeFor[tStatus]()
	f.Fuzz(func(t *testing.T, body []byte) {
		res, err := e.Enforce(Hashed, typ, body)
		switch {
		case err == nil:
			require.True(t, json.Valid(body))
			require.True(t, json.Valid(res.Body))
			checkTyped(t, h.Current(), typ, "", decodeAny(t, body), decodeAny(t, res.Body))
		case isErr[*UnknownKeyError](err):
			// The walk stops at the first unknown key or mismatch, so later syntax errors go
			// unreported.
		case isErr[*SchemaError](err):
		case isErr[*PatternError](err):
			if json.Valid(body) {
				require.True(t, verbatimMismatch(typ, decodeAny(t, body)), "*PatternError without a mismatching token")
			}
		case errors.Is(err, ErrInvalidJSON):
			require.False(t, json.Valid(body), "valid JSON rejected: %v", err)
		default:
			t.Fatalf("unexpected error %v", err)
		}

		rev, rerr := e.Enforce(Revealed, typ, body)
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
