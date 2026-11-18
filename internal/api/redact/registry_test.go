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
