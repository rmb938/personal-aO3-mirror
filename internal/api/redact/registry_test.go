package redact

import (
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

func TestRegisterTypesCollectsKeys(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.RegisterTypes(regOuter{private: "x"}, &regInner{}, []regBase{}))
	want := []string{
		"id", "hidden_field", "title", "inner", "tag_id", "tag_name", "ptrs", "grid",
		"updated_at", "node", "node_name", "children",
	}
	assert.ElementsMatch(t, want, keysOf(r))
}

func keysOf(r *Registry) []string {
	var ks []string
	for k := range r.keys {
		ks = append(ks, k)
	}
	return ks
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
	for name, v := range map[string]any{
		"no tag": noTag{}, "empty name": emptyTag{}, "camelCase": camel{}, "dash key": dash{},
		"map": withMap{}, "interface": withAny{}, "nested func": nestedBad{},
		"embedded scalar without tag": embeddedScalar{}, "top-level map": map[string]int{},
		"nil": nil,
	} {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, NewRegistry().RegisterTypes(v))
		})
	}
}

// Name is an exported non-struct type for the embedded-scalar case.
type Name string

func TestRegisterEnum(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.RegisterEnum("state", "queued"))
	require.NoError(t, r.RegisterEnum("state", "running"), "adding values is allowed")
	assert.Len(t, r.enums["state"], 2)
	require.Error(t, r.RegisterEnum("State", "x"))
	require.Error(t, r.RegisterEnum("kind"))
	require.Error(t, r.RegisterEnum("kind", ""))
}

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

func TestRegisterTypedEnums(t *testing.T) {
	type source struct {
		State srcState `json:"state"`
	}
	type job struct {
		State  *jobState  `json:"state"`
		States []srcState `json:"states"`
		Status int        `json:"status"` // a non-string field may share an enum key
	}
	type health struct {
		Status srcState `json:"status"`
	}
	r := NewRegistry()
	require.NoError(t, r.RegisterTypes(source{}, job{}, health{}))
	assert.ElementsMatch(t, []string{"active", "paused", "queued"}, setOf(r.enums["state"]), "union across types")
	assert.ElementsMatch(t, []string{"active", "paused"}, setOf(r.enums["states"]), "slice element type")
	assert.ElementsMatch(t, []string{"active", "paused"}, setOf(r.enums["status"]))

	h, err := NewHasher(0, nil)
	require.NoError(t, err)
	e, err := NewEnforcer(r, h)
	require.NoError(t, err)
	res, err := e.Enforce(Hashed, []byte(`{"state":"queued","states":["active","SENTINEL"],"status":"bogus"}`))
	require.NoError(t, err)
	assert.Contains(t, string(res.Body), `"state":"queued"`)
	assert.Contains(t, string(res.Body), `"active"`)
	assert.NotContains(t, string(res.Body), "SENTINEL")
	assert.NotContains(t, string(res.Body), "bogus")
	assert.Equal(t, []string{"states", "status"}, res.OffEnum, "off-set keys, once each")

	res, err = e.Enforce(Revealed, []byte(`{"state":"bogus","states":["bogus"]}`))
	require.NoError(t, err)
	assert.Equal(t, []string{"state", "states"}, res.OffEnum, "checked in revealed mode too")

	res, err = e.Enforce(Hashed, []byte(`{"state":"active"}`))
	require.NoError(t, err)
	assert.Empty(t, res.OffEnum)
}

func TestRegisterTypedEnumsRejects(t *testing.T) {
	type typed struct {
		State srcState `json:"state"`
	}
	type plain struct {
		State string `json:"state"`
	}
	type plainSlice struct {
		State []string `json:"state"`
	}
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
	type both struct {
		A typed `json:"a"`
		B plain `json:"b"`
	}
	for name, vals := range map[string][]any{
		"typed then plain":     {typed{}, plain{}},
		"plain then typed":     {plain{}, typed{}},
		"plain slice vs typed": {typed{}, plainSlice{}},
		"nested in one type":   {both{}},
		"no values":            {withNoVals{}},
		"empty value":          {withEmptyVal{}},
		"non-string":           {withIntEnum{}},
		"self-marshals":        {withSelfMarshal{}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, NewRegistry().RegisterTypes(vals...))
		})
	}

	t.Run("RegisterEnum after typed", func(t *testing.T) {
		r := NewRegistry()
		require.NoError(t, r.RegisterTypes(typed{}))
		assert.Error(t, r.RegisterEnum("state", "x"))
	})
	t.Run("typed after RegisterEnum", func(t *testing.T) {
		r := NewRegistry()
		require.NoError(t, r.RegisterEnum("state", "x"))
		assert.Error(t, r.RegisterTypes(typed{}))
	})
	t.Run("RegisterEnum on a plain key is the runtime-value case", func(t *testing.T) {
		r := NewRegistry()
		require.NoError(t, r.RegisterTypes(plain{}))
		assert.NoError(t, r.RegisterEnum("state", "x"))
	})
}

func setOf(m map[string]struct{}) []string {
	var s []string
	for k := range m {
		s = append(s, k)
	}
	return s
}

func TestNewEnforcerChecksEnumKeysAndSnapshots(t *testing.T) {
	h, err := NewHasher(0, nil)
	require.NoError(t, err)

	r := NewRegistry()
	require.NoError(t, r.RegisterEnum("state", "queued"))
	_, err = NewEnforcer(r, h)
	require.Error(t, err, "enum key must be a DTO key")

	type dto struct {
		State string `json:"state"`
	}
	require.NoError(t, r.RegisterTypes(dto{}))
	e, err := NewEnforcer(r, h)
	require.NoError(t, err)

	// Later registrations don't reach the enforcer.
	require.NoError(t, r.RegisterEnum("state", "running"))
	type later struct {
		Extra int `json:"extra"`
	}
	require.NoError(t, r.RegisterTypes(later{}))
	res, err := e.Enforce(Hashed, []byte(`{"state":"running"}`))
	require.NoError(t, err)
	assert.NotContains(t, string(res.Body), "running")
	_, err = e.Enforce(Hashed, []byte(`{"extra":1}`))
	require.Error(t, err)

	_, err = NewEnforcer(r, nil)
	require.Error(t, err)
}
