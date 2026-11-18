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
)

// Registry collects the JSON object keys the API may emit (from the json tags of registered
// DTO types) and the closed value sets of enum keys. Build it once at startup, then pass it to
// NewEnforcer, which takes a snapshot. It is not safe for concurrent use.
type Registry struct {
	keys  map[string]struct{}
	enums map[string]map[string]struct{}
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{keys: map[string]struct{}{}, enums: map[string]map[string]struct{}{}}
}

// RegisterTypes adds the json keys of each value's struct type, recursing into nested structs,
// pointers, slices and arrays. Every exported field needs an explicit snake_case json tag (or
// "-"). Maps, interfaces, channels and funcs are rejected: they would allow keys or shapes the
// registry can't know. Types with their own JSON or text marshaling (e.g. time.Time) are leaves.
func (r *Registry) RegisterTypes(vals ...any) error {
	for _, v := range vals {
		t := reflect.TypeOf(v)
		if t == nil {
			return errors.New("redact: RegisterTypes: nil value")
		}
		if err := r.walk(t, t.String(), map[reflect.Type]bool{}); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registry) walk(t reflect.Type, path string, seen map[reflect.Type]bool) error {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if t.Implements(jsonMarshalerType) || reflect.PointerTo(t).Implements(jsonMarshalerType) ||
		t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType) {
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
	}
	return nil
}

// RegisterEnum declares key's closed set of allowed string values; those values pass through
// the enforcer unhashed. The key must also be registered through RegisterTypes before
// NewEnforcer. Calling it again for the same key adds values.
func (r *Registry) RegisterEnum(key string, values ...string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("redact: enum key %q is not snake_case", key)
	}
	if len(values) == 0 {
		return fmt.Errorf("redact: enum %q: no values", key)
	}
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
