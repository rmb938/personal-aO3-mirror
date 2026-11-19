package redact

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// nodeKind is the JSON shape a schema node accepts.
type nodeKind uint8

const (
	kindObject nodeKind = iota + 1
	kindArray
	kindString
	kindNumber
	kindBool
	// kindOpaque is a type that marshals itself (e.g. time.Time): a string, number or bool is
	// accepted, never an object or array (its keys would be unknown).
	kindOpaque
)

// strRule is how a string node's value may pass unhashed.
type strRule uint8

const (
	ruleHash      strRule = iota // always hashed (free text)
	ruleEnum                     // in the node's enum set
	ruleTimestamp                // RFC3339 UTC (key ends in "_at")
	ruleDate                     // YYYY-MM-DD (key ends in "_on")
	rulePointer                  // a JSON Pointer into one of the node's request types
)

// node is one position in a compiled response type. Struct nodes are shared per Go type, so a
// recursive type compiles to a cycle.
type node struct {
	kind   nodeKind
	fields map[string]*node    // kindObject: json key -> field
	elem   *node               // kindArray
	rule   strRule             // kindString, kindOpaque
	enum   map[string]struct{} // ruleEnum
	ptrs   []*node             // rulePointer: the request types' struct nodes
	// path names the Go field this node is (e.g. "dto.Health.Status"): code, never content.
	path string
}

func (n *node) allows(s string) bool {
	switch n.rule {
	case ruleEnum:
		_, ok := n.enum[s]
		return ok
	case ruleTimestamp:
		return isUTCTimestamp(s)
	case ruleDate:
		return isDate(s)
	case rulePointer:
		return pointerIn(s, n.ptrs)
	default:
		return false
	}
}

// offEnum reports whether n is an enum or pointer node that does not allow s: a value our
// own code should never produce (it is hashed, and the caller warns).
func (n *node) offEnum(s string) bool {
	switch n.rule {
	case ruleEnum, rulePointer:
		return !n.allows(s)
	default:
		return false
	}
}

// maxPointerIndex bounds an array index segment (request arrays are far shorter).
const maxPointerIndex = 1_000_000

// pointerIn reports whether s is a JSON Pointer to a field of one of roots: "/" then segments,
// each a json key of the object at that position or a decimal index (no leading zeros) into an
// array, ending anywhere but the root.
func pointerIn(s string, roots []*node) bool {
	if len(s) < 2 || s[0] != '/' {
		return false
	}
	segs := strings.Split(s[1:], "/")
	for _, root := range roots {
		if walkPointer(root, segs) {
			return true
		}
	}
	return false
}

func walkPointer(n *node, segs []string) bool {
	for _, seg := range segs {
		switch n.kind {
		case kindObject:
			child, ok := n.fields[seg]
			if !ok {
				return false
			}
			n = child
		case kindArray:
			if !isIndex(seg) {
				return false
			}
			n = n.elem
		default:
			return false
		}
	}
	return true
}

func isIndex(s string) bool {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return false
	}
	v, err := strconv.Atoi(s)
	return err == nil && v >= 0 && v < maxPointerIndex && strconv.Itoa(v) == s
}

// fieldEnumKey names one struct field (by json key) that RegisterFieldEnum gave values.
type fieldEnumKey struct {
	t   reflect.Type
	key string
}

// compiler turns registered Go types into schema nodes.
type compiler struct {
	structs       map[reflect.Type]*node
	fieldEnums    map[fieldEnumKey]map[string]struct{}
	pointerFields map[fieldEnumKey][]reflect.Type
	used          map[fieldEnumKey]bool
}

func newCompiler(fieldEnums map[fieldEnumKey]map[string]struct{}, pointerFields map[fieldEnumKey][]reflect.Type) *compiler {
	return &compiler{structs: map[reflect.Type]*node{}, fieldEnums: fieldEnums, pointerFields: pointerFields,
		used: map[fieldEnumKey]bool{}}
}

// pointerNode is the node of a RegisterPointerField field (a plain string) at path.
func (c *compiler) pointerNode(t reflect.Type, path string, types []reflect.Type) (*node, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.String || implements(t, enumType) || implements(t, jsonMarshalerType) ||
		implements(t, textMarshalerType) {
		return nil, fmt.Errorf("redact: %s: RegisterPointerField on a field that is not a plain string", path)
	}
	n := &node{kind: kindString, rule: rulePointer, path: path}
	for _, rt := range types {
		root, err := c.compileStruct(rt)
		if err != nil {
			return nil, err
		}
		n.ptrs = append(n.ptrs, root)
	}
	return n, nil
}

// compile builds the node for t sitting under json key key (which picks the _at/_on rules);
// manual, when non-nil, is a RegisterFieldEnum set for this field.
func (c *compiler) compile(t reflect.Type, key, path string, manual map[string]struct{}) (*node, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if implements(t, enumType) {
		if manual != nil {
			return nil, fmt.Errorf("redact: %s: RegisterFieldEnum on an Enum-typed field", path)
		}
		if err := checkEnumType(t, path); err != nil {
			return nil, err
		}
		vals := reflect.New(t).Interface().(Enum).EnumValues()
		if len(vals) == 0 {
			return nil, fmt.Errorf("redact: %s: Enum type %s has no values", path, t)
		}
		set := make(map[string]struct{}, len(vals))
		for _, v := range vals {
			if v == "" {
				return nil, fmt.Errorf("redact: %s: Enum type %s has an empty value", path, t)
			}
			set[v] = struct{}{}
		}
		return &node{kind: kindString, rule: ruleEnum, enum: set, path: path}, nil
	}
	if implements(t, jsonMarshalerType) || implements(t, textMarshalerType) {
		if manual != nil {
			return nil, fmt.Errorf("redact: %s: RegisterFieldEnum on a self-marshaling field", path)
		}
		return &node{kind: kindOpaque, rule: suffixRule(key), path: path}, nil
	}
	switch t.Kind() {
	case reflect.String:
		if manual != nil {
			return &node{kind: kindString, rule: ruleEnum, enum: manual, path: path}, nil
		}
		return &node{kind: kindString, rule: suffixRule(key), path: path}, nil
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			// encoding/json marshals byte slices (and json/v2 byte arrays) as base64 strings.
			return nil, fmt.Errorf("redact: %s: byte slices and arrays are not allowed in a DTO", path)
		}
		elem, err := c.compile(t.Elem(), key, path, manual)
		if err != nil {
			return nil, err
		}
		return &node{kind: kindArray, elem: elem, path: path}, nil
	case reflect.Struct:
		if manual != nil {
			return nil, fmt.Errorf("redact: %s: RegisterFieldEnum on a non-string field", path)
		}
		return c.compileStruct(t)
	case reflect.Bool:
		return c.scalar(kindBool, path, manual)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return c.scalar(kindNumber, path, manual)
	default:
		return nil, fmt.Errorf("redact: %s: kind %s is not allowed in a DTO", path, t.Kind())
	}
}

func (c *compiler) scalar(k nodeKind, path string, manual map[string]struct{}) (*node, error) {
	if manual != nil {
		return nil, fmt.Errorf("redact: %s: RegisterFieldEnum on a non-string field", path)
	}
	return &node{kind: k, path: path}, nil
}

func (c *compiler) compileStruct(t reflect.Type) (*node, error) {
	if n, ok := c.structs[t]; ok {
		return n, nil
	}
	n := &node{kind: kindObject, fields: map[string]*node{}, path: t.String()}
	c.structs[t] = n
	if err := c.addFields(n, t, t, t.String()); err != nil {
		return nil, err
	}
	return n, nil
}

// addFields adds t's json fields to obj (the struct node of owner), inlining embedded structs
// the way encoding/json does. A key seen twice in one object is an error: encoding/json's
// shadowing rules would decide which field wins, and the schema must not guess.
func (c *compiler) addFields(obj *node, owner, t reflect.Type, path string) error {
	for f := range t.Fields() {
		fpath := path + "." + f.Name
		tag, ok := f.Tag.Lookup("json")
		name, opts, _ := strings.Cut(tag, ",")
		if tag == "-" {
			continue
		}
		if f.Anonymous && name == "" {
			et := f.Type
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				if err := c.addFields(obj, owner, et, fpath); err != nil {
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
		if err := checkTagOptions(opts, fpath); err != nil {
			return err
		}
		if _, dup := obj.fields[name]; dup {
			return fmt.Errorf("redact: %s: json key %q appears twice in %s", fpath, name, owner)
		}
		// A RegisterFieldEnum set is keyed by the struct type that declares the field (for a
		// promoted field, the embedded type).
		fk := fieldEnumKey{t: t, key: name}
		manual := c.fieldEnums[fk]
		if manual != nil {
			c.used[fk] = true
		}
		gopath := t.String() + "." + f.Name
		if types, ok := c.pointerFields[fk]; ok {
			c.used[fk] = true
			if manual != nil {
				return fmt.Errorf("redact: %s: both RegisterFieldEnum and RegisterPointerField", gopath)
			}
			child, err := c.pointerNode(f.Type, gopath, types)
			if err != nil {
				return err
			}
			obj.fields[name] = child
			continue
		}
		child, err := c.compile(f.Type, name, gopath, manual)
		if err != nil {
			return err
		}
		obj.fields[name] = child
	}
	return nil
}

// checkTagOptions allows only the json tag options that keep a field's JSON shape what its Go
// type says (",string" would turn a number into a string, ",inline" or ",format" reshape it).
func checkTagOptions(opts, path string) error {
	for opt := range strings.SplitSeq(opts, ",") {
		switch opt {
		case "", "omitempty", "omitzero":
		default:
			return fmt.Errorf("redact: %s: json tag option %q is not allowed in a DTO", path, opt)
		}
	}
	return nil
}

func suffixRule(key string) strRule {
	switch {
	case strings.HasSuffix(key, "_at"):
		return ruleTimestamp
	case strings.HasSuffix(key, "_on"):
		return ruleDate
	default:
		return ruleHash
	}
}

// rootType unwraps pointers: a response value and a pointer to it marshal the same.
func rootType(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}
