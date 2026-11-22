package redact

import (
	"encoding/json/v2"
	"errors"
	"reflect"
	"regexp/syntax"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tToken is a Verbatim type as a DTO package would declare it.
type tToken string

func (tToken) VerbatimPattern() string { return `tok_[a-z0-9]{8}` }

type verbBody struct {
	Token tToken   `json:"token"`
	Maybe *tToken  `json:"maybe,omitzero"`
	List  []tToken `json:"list,omitzero"`
	Note  string   `json:"note,omitzero"`
}

func newVerbatimEnforcer(t *testing.T) *Enforcer {
	t.Helper()
	reg := NewRegistry()
	require.NoError(t, reg.RegisterTypes(verbBody{}))
	h, err := NewHasher(0, nil)
	require.NoError(t, err)
	e, err := NewEnforcer(reg, h)
	require.NoError(t, err)
	return e
}

func TestVerbatimField(t *testing.T) {
	e := newVerbatimEnforcer(t)
	typ := reflect.TypeFor[verbBody]()
	tok := tToken("tok_abcd1234")
	body, err := json.Marshal(verbBody{Token: tok, Maybe: &tok, List: []tToken{"tok_00000000", tok}})
	require.NoError(t, err)
	for _, mode := range []Mode{Hashed, Revealed} {
		res, err := e.Enforce(mode, typ, body)
		require.NoError(t, err, mode)
		assert.JSONEq(t, string(body), string(res.Body), "%s: a match passes unhashed", mode)
		assert.Empty(t, res.OffEnum)
	}

	// The same string in a plain field is free text: hashed.
	res, err := e.Enforce(Hashed, typ, []byte(`{"token":"tok_abcd1234","note":"tok_abcd1234"}`))
	require.NoError(t, err)
	assert.Regexp(t, `^\{"token":"tok_abcd1234","note":"h_[0-9a-f]{24}"\}$`, string(res.Body))
}

func TestVerbatimMismatchFails(t *testing.T) {
	e := newVerbatimEnforcer(t)
	typ := reflect.TypeFor[verbBody]()
	for _, v := range []string{
		"SENTINEL-x", "", "tok_abcd123", "tok_abcd12345", "xtok_abcd1234", "tok_abcd1234\n",
		"\ntok_abcd1234", "TOK_ABCD1234", "tok_abcd 234", "tok_abcd1234 SENTINEL", "tok_abcd123é",
	} {
		q, err := json.Marshal(v)
		require.NoError(t, err)
		for _, in := range []string{
			`{"token":` + string(q) + `}`,
			`{"token":"tok_abcd1234","maybe":` + string(q) + `}`,
			`{"token":"tok_abcd1234","list":["tok_abcd1234",` + string(q) + `]}`,
		} {
			for _, mode := range []Mode{Hashed, Revealed} {
				_, err := e.Enforce(mode, typ, []byte(in))
				pe, ok := errors.AsType[*PatternError](err)
				require.True(t, ok, "%s %q: want *PatternError, got %v", mode, v, err)
				assert.Contains(t, pe.Path, "redact.verbBody.")
				assert.NotContains(t, err.Error(), "SENTINEL", "the value is never in the error")
			}
		}
	}
	_, err := e.Enforce(Hashed, typ, []byte(`{"token":5}`))
	assert.True(t, isErr[*SchemaError](err), "a number where a Verbatim string belongs: %v", err)
	res, err := e.Enforce(Hashed, typ, []byte(`{"token":null}`))
	require.NoError(t, err, "null is allowed anywhere")
	assert.JSONEq(t, `{"token":null}`, string(res.Body))
}

// Verbatim types the registry must refuse.
type (
	vInt        int
	vSelf       string
	vEnum       string
	vSyntax     string
	vDot        string
	vStar       string
	vPlus       string
	vOpenRepeat string
	vSpace      string
	vSpaceEsc   string
	vFold       string
	vNonASCII   string
	vUnicode    string
	vNegated    string
	vAnchor     string
	vWordB      string
	vEmpty      string
	vTooLong    string
)

func (vInt) VerbatimPattern() string        { return `[0-9]{1,3}` }
func (vSelf) VerbatimPattern() string       { return `[a-z]{1,3}` }
func (vSelf) MarshalText() ([]byte, error)  { return []byte("x"), nil }
func (vEnum) VerbatimPattern() string       { return `[a-z]{1,3}` }
func (vEnum) EnumValues() []string          { return []string{"a"} }
func (vSyntax) VerbatimPattern() string     { return `[a-z` }
func (vDot) VerbatimPattern() string        { return `a.{1,3}` }
func (vStar) VerbatimPattern() string       { return `a[a-z]*` }
func (vPlus) VerbatimPattern() string       { return `[a-z]+` }
func (vOpenRepeat) VerbatimPattern() string { return `[a-z]{2,}` }
func (vSpace) VerbatimPattern() string      { return `[a-z ]{1,8}` }
func (vSpaceEsc) VerbatimPattern() string   { return `a\s{1,3}` }
func (vFold) VerbatimPattern() string       { return `(?i)k{1,3}` }
func (vNonASCII) VerbatimPattern() string   { return `é{1,3}` }
func (vUnicode) VerbatimPattern() string    { return `\pL{1,3}` }
func (vNegated) VerbatimPattern() string    { return `[^a]{1,3}` }
func (vAnchor) VerbatimPattern() string     { return `^a{1,3}` }
func (vWordB) VerbatimPattern() string      { return `\ba{1,3}` }
func (vEmpty) VerbatimPattern() string      { return `a{0,3}` }
func (vTooLong) VerbatimPattern() string    { return `[a-z]{1000}(?:[a-z]{10}){3}` }

func TestVerbatimRejects(t *testing.T) {
	wrap := func(v any) any {
		return reflect.New(reflect.StructOf([]reflect.StructField{{
			Name: "F", Type: reflect.TypeOf(v), Tag: `json:"f"`,
		}})).Elem().Interface()
	}
	for name, v := range map[string]any{
		"int kind": vInt(0), "marshals itself": vSelf(""), "also an Enum": vEnum(""),
		"bad syntax": vSyntax(""), "dot": vDot(""), "star": vStar(""), "plus": vPlus(""),
		"open repeat": vOpenRepeat(""), "space": vSpace(""), `\s`: vSpaceEsc(""),
		"case folding": vFold(""), "non-ASCII literal": vNonASCII(""), "unicode class": vUnicode(""),
		"negated class": vNegated(""), "anchor": vAnchor(""), "word boundary": vWordB(""),
		"matches empty": vEmpty(""), "too long": vTooLong(""),
	} {
		require.Error(t, NewRegistry().RegisterTypes(wrap(v)), name)
	}

	h, err := NewHasher(0, nil)
	require.NoError(t, err)
	reg := NewRegistry()
	require.NoError(t, reg.RegisterTypes(verbBody{}))
	require.NoError(t, reg.RegisterFieldEnum(verbBody{}, "token", "x"))
	_, err = NewEnforcer(reg, h)
	require.Error(t, err, "RegisterFieldEnum on a Verbatim field")

	reg = NewRegistry()
	require.NoError(t, reg.RegisterTypes(verbBody{}))
	require.NoError(t, reg.RegisterPointerField(verbBody{}, "token"))
	_, err = NewEnforcer(reg, h)
	assert.Error(t, err, "RegisterPointerField on a Verbatim field")
}

func TestVerbatimMaxLen(t *testing.T) {
	for pat, want := range map[string]int{
		`abc`: 3, `[a-z]{4}`: 4, `a(?:bc|d)?`: 3, `(x{2,5}|[0-9]{1,7})-y`: 9,
		`tok_[a-z0-9]{8}`: 12, `(?:[a-z]{1,3}\.){0,2}[a-z]{2}`: 10,
	} {
		re, err := syntax.Parse(pat, syntax.Perl)
		require.NoError(t, err, pat)
		got, err := verbatimMaxLen(re)
		require.NoError(t, err, pat)
		assert.Equal(t, want, got, pat)
	}
}
