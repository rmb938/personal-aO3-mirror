package redact

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// keyPattern is the API's snake_case key convention.
var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

var (
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	enumType          = reflect.TypeFor[Enum]()
)

// Enum is a string type with a closed value set. A DTO field whose type (after pointers,
// slices and arrays) implements it registers its json key as an enum with the zero value's
// EnumValues. DTO packages satisfy it structurally and never import redact.
type Enum interface {
	EnumValues() []string
}

// Registry collects the JSON object keys the API may emit (from the json tags of registered
// DTO types) and the closed value sets of enum keys. Build it once at startup, then pass it to
// NewEnforcer, which takes a snapshot. It is not safe for concurrent use.
type Registry struct {
	keys  map[string]struct{}
	enums map[string]map[string]struct{}
	// typed, plain and manual record where each enum-relevant key came from: a typed Enum
	// field (with the field's path), a plain string field, or RegisterEnum. typed may not
	// mix with the other two.
	typed  map[string]string
	plain  map[string]string
	manual map[string]bool
	// roots are the registered types (pointers unwrapped) that EnforceAs accepts, and
	// fieldEnums the RegisterFieldEnum sets; NewEnforcer compiles both into schemas.
	roots      []reflect.Type
	fieldEnums map[fieldEnumKey]map[string]struct{}
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		keys:       map[string]struct{}{},
		enums:      map[string]map[string]struct{}{},
		typed:      map[string]string{},
		plain:      map[string]string{},
		manual:     map[string]bool{},
		fieldEnums: map[fieldEnumKey]map[string]struct{}{},
	}
}

// RegisterTypes adds the json keys of each value's struct type, recursing into nested structs,
// pointers, slices and arrays. Every exported field needs an explicit snake_case json tag (or
// "-"). Maps, interfaces, channels and funcs are rejected: they would allow keys or shapes the
// registry can't know. Types with their own JSON or text marshaling (e.g. time.Time) are leaves.
//
// A field whose type implements Enum makes its key an enum (the union of values across every
// Enum type on that key). It is an error for an Enum to be a non-string kind, to marshal
// itself, to list no values or an empty value, or for its key to also be a plain string field
// or a RegisterEnum key: either would let enum-looking values through unchecked.
func (r *Registry) RegisterTypes(vals ...any) error {
	for _, v := range vals {
		t := reflect.TypeOf(v)
		if t == nil {
			return errors.New("redact: RegisterTypes: nil value")
		}
		if err := r.walk(t, t.String(), map[reflect.Type]bool{}); err != nil {
			return err
		}
		r.roots = append(r.roots, rootType(t))
	}
	return nil
}

// RegisterFieldEnum declares the closed value set of one plain string field for EnforceAs:
// the field with json key key in v's struct type (the type that declares it, for a promoted
// field). It is for values known only at runtime (e.g. the build version); a fixed set belongs
// on an Enum type. The field must exist and be a string, slice of strings or pointer to one,
// checked by NewEnforcer. Calling it again for the same field adds values.
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

// leaf unwraps pointers, slices and arrays.
func leaf(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	return t
}

func implements(t, iface reflect.Type) bool {
	return t.Implements(iface) || reflect.PointerTo(t).Implements(iface)
}

func (r *Registry) walk(t reflect.Type, path string, seen map[reflect.Type]bool) error {
	t = leaf(t)
	if implements(t, enumType) {
		return checkEnumType(t, path)
	}
	if implements(t, jsonMarshalerType) || implements(t, textMarshalerType) {
		return nil
	}
	switch t.Kind() {
	case reflect.Map, reflect.Interface, reflect.Chan, reflect.Func, reflect.UnsafePointer,
		reflect.Complex64, reflect.Complex128:
		return fmt.Errorf("redact: %s: kind %s is not allowed in a DTO", path, t.Kind())
	case reflect.Struct:
	default:
		return nil // bool, numbers, string
	}
	if seen[t] {
		return nil
	}
	seen[t] = true
	for f := range t.Fields() {
		fpath := path + "." + f.Name
		tag, ok := f.Tag.Lookup("json")
		name, _, _ := strings.Cut(tag, ",")
		if tag == "-" {
			continue
		}
		if f.Anonymous && name == "" {
			et := f.Type
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				// Embedded struct without a name (exported or not): encoding/json promotes
				// its fields.
				if err := r.walk(et, fpath, seen); err != nil {
					return err
				}
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if !ok || name == "" {
			return fmt.Errorf("redact: %s: exported field needs a json tag", fpath)
		}
		if !keyPattern.MatchString(name) {
			return fmt.Errorf("redact: %s: json key %q is not snake_case", fpath, name)
		}
		r.keys[name] = struct{}{}
		if err := r.walk(f.Type, fpath, seen); err != nil {
			return err
		}
		if err := r.classify(name, leaf(f.Type), fpath); err != nil {
			return err
		}
	}
	return nil
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

// classify records a string field's key as typed-enum or plain, adding a typed enum's values.
// Non-string fields don't matter to enums (the enforcer only checks strings).
func (r *Registry) classify(key string, t reflect.Type, path string) error {
	if t.Kind() != reflect.String {
		return nil
	}
	if !implements(t, enumType) {
		if prev, ok := r.typed[key]; ok {
			return fmt.Errorf("redact: %s: key %q is a plain string here but an Enum at %s", path, key, prev)
		}
		r.plain[key] = path
		return nil
	}
	if prev, ok := r.plain[key]; ok {
		return fmt.Errorf("redact: %s: key %q is an Enum here but a plain string at %s", path, key, prev)
	}
	if r.manual[key] {
		return fmt.Errorf("redact: %s: key %q is an Enum and also a RegisterEnum key", path, key)
	}
	vals := reflect.New(t).Interface().(Enum).EnumValues()
	if len(vals) == 0 {
		return fmt.Errorf("redact: %s: Enum type %s has no values", path, t)
	}
	if err := r.addEnum(key, vals); err != nil {
		return fmt.Errorf("%w (at %s)", err, path)
	}
	r.typed[key] = path
	return nil
}

// RegisterEnum declares key's closed set of allowed string values; those values pass through
// the enforcer unhashed. It is for values known only at runtime (e.g. the build version);
// a fixed set belongs on an Enum type. The key must also be registered through RegisterTypes
// before NewEnforcer, and must not be a typed Enum key. Calling it again for the same key adds
// values.
func (r *Registry) RegisterEnum(key string, values ...string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("redact: enum key %q is not snake_case", key)
	}
	if len(values) == 0 {
		return fmt.Errorf("redact: enum %q: no values", key)
	}
	if prev, ok := r.typed[key]; ok {
		return fmt.Errorf("redact: enum %q: already an Enum type at %s", key, prev)
	}
	if err := r.addEnum(key, values); err != nil {
		return err
	}
	r.manual[key] = true
	return nil
}

// addEnum adds values to key's set.
func (r *Registry) addEnum(key string, values []string) error {
	set := r.enums[key]
	if set == nil {
		set = map[string]struct{}{}
		r.enums[key] = set
	}
	for _, v := range values {
		if v == "" {
			return fmt.Errorf("redact: enum %q: empty value", key)
		}
		set[v] = struct{}{}
	}
	return nil
}
