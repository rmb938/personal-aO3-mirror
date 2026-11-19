package redact

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"time"
)

// keyPattern is the API's snake_case key convention.
var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

var (
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	enumType          = reflect.TypeFor[Enum]()
	durationIface     = reflect.TypeFor[Duration]()
)

// Duration is an int64 type that marshals itself as a Go duration string (time.Duration's
// String form, e.g. "1h30m0s"). A DTO field of such a type is a JSON string that passes
// unhashed only when it is exactly such a string: its grammar (digits, '.', '-', unit
// letters) can't carry content. DTO packages satisfy it structurally and never import redact.
type Duration interface {
	encoding.TextMarshaler
	Duration() time.Duration
}

// Enum is a string type with a closed value set. A DTO field whose type (after pointers,
// slices and arrays) implements it lets exactly the zero value's EnumValues through unhashed,
// at that field only. DTO packages satisfy it structurally and never import redact.
type Enum interface {
	EnumValues() []string
}

// Registry collects the DTO types the API may return and the runtime value sets of plain
// string fields. Build it once at startup, then pass it to NewEnforcer, which takes a
// snapshot. It is not safe for concurrent use.
type Registry struct {
	// roots are the registered types (pointers unwrapped) that Enforce accepts.
	roots      []reflect.Type
	fieldEnums map[fieldEnumKey]map[string]struct{}
	// pointerFields are plain string fields that hold JSON Pointers into these request types.
	pointerFields map[fieldEnumKey][]reflect.Type
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{fieldEnums: map[fieldEnumKey]map[string]struct{}{}, pointerFields: map[fieldEnumKey][]reflect.Type{}}
}

// RegisterPointerField lets the plain string field key of struct v hold an RFC 6901 JSON
// Pointer (e.g. "/filters/2/tag_id") into one of requestTypes: it passes unhashed only when
// each segment is a json key of the struct at that position or a decimal index into a slice or
// array, and it ends at a field. Keys are snake_case, so a segment needing ~0/~1 escapes never
// matches. requestTypes follow the RegisterTypes rules; with none, every pointer is hashed.
func (r *Registry) RegisterPointerField(v any, key string, requestTypes ...any) error {
	t := rootType(reflect.TypeOf(v))
	if t == nil || t.Kind() != reflect.Struct {
		return fmt.Errorf("redact: RegisterPointerField: %T is not a struct", v)
	}
	fk := fieldEnumKey{t: t, key: key}
	if _, dup := r.pointerFields[fk]; dup {
		return fmt.Errorf("redact: RegisterPointerField %s.%s: registered twice", t, key)
	}
	types := make([]reflect.Type, 0, len(requestTypes))
	for _, rv := range requestTypes {
		rt := rootType(reflect.TypeOf(rv))
		if rt == nil || rt.Kind() != reflect.Struct {
			return fmt.Errorf("redact: RegisterPointerField %s.%s: request type %T is not a struct", t, key, rv)
		}
		if _, err := newCompiler(nil, nil).compile(rt, "", rt.String(), nil); err != nil {
			return err
		}
		types = append(types, rt)
	}
	r.pointerFields[fk] = types
	return nil
}

// RegisterTypes registers each value's type as a response type for Enforce, checking that its
// JSON shape is fully known: every exported field (in nested structs, pointers, slices and
// arrays too) needs an explicit snake_case json tag (or "-") with no options but omitempty and
// omitzero, and no key may appear twice in one struct. Maps, interfaces, channels, funcs and
// byte slices or arrays are rejected: they would allow keys or shapes the schema can't know.
// Types with their own JSON or text marshaling (e.g. time.Time) are leaves.
//
// A field whose type implements Enum allows its own values. It is an error for an Enum to be a
// non-string kind, to marshal itself, or to list no values or an empty value.
func (r *Registry) RegisterTypes(vals ...any) error {
	for _, v := range vals {
		t := rootType(reflect.TypeOf(v))
		if t == nil {
			return errors.New("redact: RegisterTypes: nil value")
		}
		if _, err := newCompiler(nil, nil).compile(t, "", t.String(), nil); err != nil {
			return err
		}
		r.roots = append(r.roots, t)
	}
	return nil
}

func implements(t, iface reflect.Type) bool {
	return t.Implements(iface) || reflect.PointerTo(t).Implements(iface)
}

// checkEnumType rejects Enum types the enforcer can't check by value: a non-string kind, or
// one that marshals itself (its JSON need not be its value).
func checkEnumType(t reflect.Type, path string) error {
	if t.Kind() != reflect.String {
		return fmt.Errorf("redact: %s: Enum type %s has kind %s, not string", path, t, t.Kind())
	}
	if implements(t, jsonMarshalerType) || implements(t, textMarshalerType) {
		return fmt.Errorf("redact: %s: Enum type %s must not marshal itself", path, t)
	}
	return nil
}

// RegisterFieldEnum declares the closed value set of one plain string field: the field with
// json key key in v's struct type (the type that declares it, for a promoted field). It is for
// values known only at runtime (e.g. the build version); a fixed set belongs on an Enum type.
// The field must exist in a registered type and be a string, slice of strings or pointer to
// one, checked by NewEnforcer. Calling it again for the same field adds values.
func (r *Registry) RegisterFieldEnum(v any, key string, values ...string) error {
	t := rootType(reflect.TypeOf(v))
	if t == nil || t.Kind() != reflect.Struct {
		return fmt.Errorf("redact: RegisterFieldEnum: %T is not a struct", v)
	}
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("redact: RegisterFieldEnum: key %q is not snake_case", key)
	}
	if len(values) == 0 {
		return fmt.Errorf("redact: RegisterFieldEnum %s.%s: no values", t, key)
	}
	fk := fieldEnumKey{t: t, key: key}
	set := r.fieldEnums[fk]
	if set == nil {
		set = map[string]struct{}{}
		r.fieldEnums[fk] = set
	}
	for _, s := range values {
		if s == "" {
			return fmt.Errorf("redact: RegisterFieldEnum %s.%s: empty value", t, key)
		}
		set[s] = struct{}{}
	}
	return nil
}
