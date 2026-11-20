package redact

import (
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type regInner struct {
	TagID   int64  `json:"tag_id"`
	TagName string `json:"tag_name,omitempty"`
}

type regBase struct {
	ID int64 `json:"id"`
}

type regHidden struct {
	HiddenField string `json:"hidden_field"`
}

type regNode struct {
	Name     string     `json:"node_name"`
	Children []*regNode `json:"children"`
}

type regOuter struct {
	regBase
	regHidden
	Title     string       `json:"title"`
	Inner     regInner     `json:"inner"`
	Ptrs      []*regInner  `json:"ptrs"`
	Grid      [][2]float64 `json:"grid"`
	UpdatedAt time.Time    `json:"updated_at"`
	Node      *regNode     `json:"node"`
	Skipped   string       `json:"-"`
	private   string
}

func newEnforcerFor(t *testing.T, vals ...any) *Enforcer {
	t.Helper()
	h, err := NewHasher(0, nil)
	require.NoError(t, err)
	r := NewRegistry()
	require.NoError(t, r.RegisterTypes(vals...))
	e, err := NewEnforcer(r, h)
	require.NoError(t, err)
	return e
}

func fieldsOf(n *node) []string {
	return slices.Sorted(maps.Keys(n.fields))
}

func TestRegisterTypesSchemas(t *testing.T) {
	e := newEnforcerFor(t, regOuter{private: "x"}, &regInner{}, []regBase{})
	outer := e.schemas[reflect.TypeFor[regOuter]()]
	require.NotNil(t, outer)
	assert.Equal(t, []string{"grid", "hidden_field", "id", "inner", "node", "ptrs", "title", "updated_at"},
		fieldsOf(outer), "embedded fields promoted, - and unexported skipped")
	assert.Equal(t, []string{"tag_id", "tag_name"}, fieldsOf(outer.fields["inner"]))
	assert.Same(t, outer.fields["inner"], outer.fields["ptrs"].elem, "struct nodes are shared per type")
	node := outer.fields["node"]
	assert.Same(t, node, node.fields["children"].elem, "recursive type is a cycle")
	assert.Equal(t, kindNumber, outer.fields["grid"].elem.elem.kind)
	assert.Equal(t, kindOpaque, outer.fields["updated_at"].kind)
	assert.Equal(t, ruleTimestamp, outer.fields["updated_at"].rule)

	assert.NotNil(t, e.schemas[reflect.TypeFor[regInner]()], "pointer roots unwrapped")
	assert.Equal(t, kindArray, e.schemas[reflect.TypeFor[[]regBase]()].kind)
}

func TestRegisterTypesRejects(t *testing.T) {
	type noTag struct {
		Title string
	}
	type emptyTag struct {
		Title string `json:",omitempty"`
	}
	type camel struct {
		Title string `json:"workTitle"`
	}
	type dash struct {
		Title string `json:"-,"`
	}
	type withMap struct {
		M map[string]string `json:"m"`
	}
	type withAny struct {
		A any `json:"a"`
	}
	type nestedBad struct {
		In []struct {
			F func() `json:"f"`
		} `json:"in"`
	}
	type embeddedScalar struct {
		Name
	}
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
	type withComplex struct {
		C complex64 `json:"c"`
	}
	for name, v := range map[string]any{
		"no tag": noTag{}, "empty name": emptyTag{}, "camelCase": camel{}, "dash key": dash{},
		"map": withMap{}, "interface": withAny{}, "nested func": nestedBad{},
		"embedded scalar without tag": embeddedScalar{}, "top-level map": map[string]int{},
		"nil": nil, "byte slice": withBytes{}, "byte array": withByteArray{},
		"string option": withStringOpt{}, "format option": withFormat{},
		"duplicate key via embedding": withDup{}, "complex": withComplex{},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, NewRegistry().RegisterTypes(v))
		})
	}
}

// Name is an exported non-struct type for the embedded-scalar case.
type Name string

type srcState string

func (srcState) EnumValues() []string { return []string{"active", "paused"} }

type jobState string

func (*jobState) EnumValues() []string { return []string{"queued", "paused"} } // pointer receiver

type noVals string

func (noVals) EnumValues() []string { return nil }

type emptyVal string

func (emptyVal) EnumValues() []string { return []string{"a", ""} }

type intEnum int

func (intEnum) EnumValues() []string { return []string{"1"} }

type selfMarshal string

func (selfMarshal) EnumValues() []string           { return []string{"a"} }
func (s selfMarshal) MarshalText() ([]byte, error) { return []byte("x" + s), nil }

// Enum fields sharing a key each keep their own set, and may share it with a plain field.
func TestRegisterTypedEnumsPerField(t *testing.T) {
	type source struct {
		State srcState `json:"state"`
	}
	type job struct {
		State  *jobState  `json:"state"`
		States []srcState `json:"states"`
		Status int        `json:"status"`
	}
	type note struct {
		State string `json:"state"` // free text under an enum key elsewhere
	}
	type all struct {
		Source source `json:"source"`
		Job    job    `json:"job"`
		Note   note   `json:"note"`
	}
	e := newEnforcerFor(t, all{})
	typ := reflect.TypeFor[all]()
	in := `{"source":{"state":"queued"},"job":{"state":"queued","states":["active","SENTINEL"],"status":1},` +
		`"note":{"state":"active"}}`
	res, err := e.Enforce(Hashed, typ, []byte(in))
	require.NoError(t, err)
	body := string(res.Body)
	assert.Contains(t, body, `"job":{"state":"queued"`, "job state's own value")
	assert.NotContains(t, body, `"source":{"state":"queued"}`, "job value in source state hashed")
	assert.Contains(t, body, `["active",`)
	assert.NotContains(t, body, "SENTINEL")
	assert.Contains(t, body, `"note":{"state":"h_`, "plain field hashed")
	assert.Equal(t, []string{"redact.source.State", "redact.job.States"}, res.OffEnum)

	res, err = e.Enforce(Hashed, typ, []byte(`{"source":{"state":"paused"},"job":{"state":"paused"}}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"source":{"state":"paused"},"job":{"state":"paused"}}`, string(res.Body))
	assert.Empty(t, res.OffEnum)
}

func TestRegisterTypedEnumsRejects(t *testing.T) {
	type withNoVals struct {
		V noVals `json:"v"`
	}
	type withEmptyVal struct {
		V emptyVal `json:"v"`
	}
	type withIntEnum struct {
		V []intEnum `json:"v"`
	}
	type withSelfMarshal struct {
		V selfMarshal `json:"v"`
	}
	for name, v := range map[string]any{
		"no values": withNoVals{}, "empty value": withEmptyVal{}, "non-string": withIntEnum{},
		"self-marshals": withSelfMarshal{},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, NewRegistry().RegisterTypes(v))
		})
	}
}

func TestNewEnforcerSnapshots(t *testing.T) {
	h, err := NewHasher(0, nil)
	require.NoError(t, err)
	type dto struct {
		State string `json:"state"`
	}
	type later struct {
		Extra int `json:"extra"`
	}
	r := NewRegistry()
	require.NoError(t, r.RegisterTypes(dto{}))
	e, err := NewEnforcer(r, h)
	require.NoError(t, err)

	require.NoError(t, r.RegisterTypes(later{}))
	_, err = e.Enforce(Hashed, reflect.TypeFor[later](), []byte(`{"extra":1}`))
	require.ErrorContains(t, err, "not registered", "later registrations don't reach the enforcer")

	_, err = NewEnforcer(r, nil)
	require.Error(t, err)
}
