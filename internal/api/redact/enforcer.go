package redact

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"
)

// Mode selects whether the enforcer hashes strings.
type Mode int

const (
	// Hashed replaces every string that is not provably safe with a keyed hash (the default).
	Hashed Mode = iota
	// Revealed checks the structure (keys) but returns strings as they are. Only a request
	// with the reveal param gets it.
	Revealed
)

func (m Mode) String() string {
	switch m {
	case Hashed:
		return "hashed"
	case Revealed:
		return "revealed"
	default:
		return fmt.Sprintf("Mode(%d)", int(m))
	}
}

// UnknownKeyError reports an object key that no registered DTO declares. It carries only the
// key's hash, never the key.
type UnknownKeyError struct {
	KeyHash string
}

func (e *UnknownKeyError) Error() string {
	return "redact: unregistered JSON key " + e.KeyHash
}

// ErrInvalidJSON wraps input that is not exactly one well-formed JSON value. Its message never
// holds the input.
var ErrInvalidJSON = errors.New("redact: invalid JSON")

// Result is an enforced response body.
type Result struct {
	Body []byte
	// Epoch is the ID of the key epoch that hashed Body (the X-Fanwork-Key-Epoch header). It
	// is empty in Revealed mode.
	Epoch string
	// OffEnum lists, once each and in order of first sight, the enum keys that held a value
	// outside their set (hashed in Hashed mode, like any string). It is a programming error
	// (a model value missing from its DTO type) for the caller to log: keys are registered
	// DTO keys, never content.
	OffEnum []string
}

// SchemaError reports a JSON value whose shape doesn't match the Go type it was enforced as
// (e.g. an object where the field is a string). It names only the Go field, never content.
type SchemaError struct {
	Path string
}

func (e *SchemaError) Error() string {
	return "redact: JSON does not match " + e.Path
}

// Enforcer rewrites JSON response bodies according to a registry snapshot. It is safe for
// concurrent use.
type Enforcer struct {
	hasher *Hasher
	keys   map[string]struct{}
	enums  map[string]map[string]struct{}
	// schemas are the compiled registered types, for EnforceAs.
	schemas map[reflect.Type]*node
}

// NewEnforcer snapshots reg (later registrations don't affect it), checks that every enum key
// is a registered key, and compiles each registered type into a schema for EnforceAs. Every
// RegisterFieldEnum field must exist in a registered type.
func NewEnforcer(reg *Registry, h *Hasher) (*Enforcer, error) {
	if h == nil {
		return nil, errors.New("redact: NewEnforcer: nil hasher")
	}
	e := &Enforcer{
		hasher: h, keys: maps.Clone(reg.keys), enums: make(map[string]map[string]struct{}, len(reg.enums)),
		schemas: make(map[reflect.Type]*node, len(reg.roots)),
	}
	for k, set := range reg.enums {
		if _, ok := e.keys[k]; !ok {
			return nil, fmt.Errorf("redact: enum key %q is not a registered DTO key", k)
		}
		e.enums[k] = maps.Clone(set)
	}
	fieldEnums := make(map[fieldEnumKey]map[string]struct{}, len(reg.fieldEnums))
	for fk, set := range reg.fieldEnums {
		fieldEnums[fk] = maps.Clone(set)
	}
	c := newCompiler(fieldEnums)
	for _, t := range reg.roots {
		n, err := c.compile(t, "", t.String(), nil)
		if err != nil {
			return nil, err
		}
		e.schemas[t] = n
	}
	for fk := range fieldEnums {
		if !c.used[fk] {
			return nil, fmt.Errorf("redact: RegisterFieldEnum %s.%s: no such field in a registered type", fk.t, fk.key)
		}
	}
	return e, nil
}

// Enforce checks and rewrites one JSON value. Every object key must be registered (else
// *UnknownKeyError). In Hashed mode a string value passes through only when allowed for the
// key it sits under (directly, or inside arrays): an enum value in the key's set, an RFC3339
// UTC timestamp under a key ending in "_at", or a YYYY-MM-DD date under a key ending in "_on".
// Every other string becomes "h_" + 24 hex. Numbers, booleans and null pass through. The whole
// body is hashed with one epoch.
func (e *Enforcer) Enforce(mode Mode, in []byte) (Result, error) {
	return e.enforce(mode, nil, in)
}

// EnforceAs is Enforce driven by the schema of t, a type registered through RegisterTypes
// (or a pointer to one), whose marshaled value in is. Each object must be the struct at that
// position: a key it doesn't declare is an *UnknownKeyError, and a value of the wrong JSON kind
// a *SchemaError (null is always allowed). A string passes unhashed only when its own field
// allows it: a value of the field's Enum type or RegisterFieldEnum set, or an RFC3339 UTC
// timestamp or YYYY-MM-DD date under a key ending in "_at" or "_on". So two fields sharing a
// key (two `status` enums) each allow only their own values. Result.OffEnum lists Go field
// paths instead of keys.
func (e *Enforcer) EnforceAs(mode Mode, t reflect.Type, in []byte) (Result, error) {
	n, ok := e.schemas[rootType(t)]
	if !ok {
		return Result{}, fmt.Errorf("redact: type %v is not registered", t)
	}
	return e.enforce(mode, n, in)
}

// enforce walks in against n, or by key alone when n is nil (Enforce).
func (e *Enforcer) enforce(mode Mode, n *node, in []byte) (Result, error) {
	if mode != Hashed && mode != Revealed {
		return Result{}, fmt.Errorf("redact: unknown mode %d", int(mode))
	}
	ep := e.hasher.Current()
	w := walker{e: e, mode: mode, mac: ep.newMac()}
	w.out.Grow(len(in) + len(in)/4)
	dec := json.NewDecoder(bytes.NewReader(in))
	dec.UseNumber()
	if err := w.value(dec, "", n); err != nil {
		return Result{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Result{}, fmt.Errorf("%w: trailing data", ErrInvalidJSON)
	}
	res := Result{Body: w.out.Bytes(), OffEnum: w.offEnum}
	if mode == Hashed {
		res.Epoch = ep.ID()
	}
	return res, nil
}

type walker struct {
	e    *Enforcer
	mode Mode
	mac  *mac
	out  bytes.Buffer
	// offEnum collects Result.OffEnum.
	offEnum []string
}

// value reads and writes one JSON value; key is the object key it sits under ("" at the top)
// and n its schema node (nil when enforcing by key alone).
func (w *walker) value(dec *json.Decoder, key string, n *node) error {
	tok, err := dec.Token()
	if err != nil {
		return invalid(err)
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			if err := n.want(kindObject); err != nil {
				return err
			}
			return w.object(dec, n)
		case '[':
			if err := n.want(kindArray); err != nil {
				return err
			}
			var elem *node
			if n != nil {
				elem = n.elem
			}
			return w.array(dec, key, elem)
		default:
			return fmt.Errorf("%w: unexpected %c", ErrInvalidJSON, rune(t))
		}
	case string:
		if err := n.want(kindString); err != nil {
			return err
		}
		w.noteOffEnum(key, n, t)
		if w.mode == Revealed || w.e.allowed(key, n, t) {
			w.writeString(t)
		} else {
			w.writeRawString(w.mac.hash(t))
		}
	case json.Number:
		if err := n.want(kindNumber); err != nil {
			return err
		}
		w.out.WriteString(string(t))
	case bool:
		if err := n.want(kindBool); err != nil {
			return err
		}
		if t {
			w.out.WriteString("true")
		} else {
			w.out.WriteString("false")
		}
	case nil:
		w.out.WriteString("null")
	default:
		return fmt.Errorf("%w: unexpected token %T", ErrInvalidJSON, tok)
	}
	return nil
}

// want checks that a JSON value of kind k may sit at n (any value may when n is nil). An opaque
// node takes any scalar.
func (n *node) want(k nodeKind) error {
	if n == nil || n.kind == k || (n.kind == kindOpaque && k != kindObject && k != kindArray) {
		return nil
	}
	return &SchemaError{Path: n.path}
}

// object walks an object at n (nil: keys are checked against every registered key).
func (w *walker) object(dec *json.Decoder, n *node) error {
	w.out.WriteByte('{')
	for i := 0; dec.More(); i++ {
		tok, err := dec.Token()
		if err != nil {
			return invalid(err)
		}
		k, ok := tok.(string)
		if !ok {
			return fmt.Errorf("%w: object key is %T", ErrInvalidJSON, tok)
		}
		var child *node
		if n != nil {
			child, ok = n.fields[k]
		} else {
			_, ok = w.e.keys[k]
		}
		if !ok {
			return &UnknownKeyError{KeyHash: w.mac.hash(k)}
		}
		if i > 0 {
			w.out.WriteByte(',')
		}
		w.writeRawString(k) // registered keys are snake_case: no escaping needed
		w.out.WriteByte(':')
		if err := w.value(dec, k, child); err != nil {
			return err
		}
	}
	return w.closing(dec, '}')
}

// array walks an array whose elements sit at elem (nil when enforcing by key alone).
func (w *walker) array(dec *json.Decoder, key string, elem *node) error {
	w.out.WriteByte('[')
	for i := 0; dec.More(); i++ {
		if i > 0 {
			w.out.WriteByte(',')
		}
		if err := w.value(dec, key, elem); err != nil {
			return err
		}
	}
	return w.closing(dec, ']')
}

func (w *walker) closing(dec *json.Decoder, want byte) error {
	tok, err := dec.Token()
	if err != nil {
		return invalid(err)
	}
	if tok != json.Delim(want) {
		return fmt.Errorf("%w: expected %c", ErrInvalidJSON, want)
	}
	w.out.WriteByte(want)
	return nil
}

func (w *walker) writeString(s string) {
	b, _ := json.Marshal(s) // marshaling a string never fails
	w.out.Write(b)
}

// writeRawString writes s, which needs no escaping (a hash or a registered key), quoted.
func (w *walker) writeRawString(s string) {
	w.out.WriteByte('"')
	w.out.WriteString(s)
	w.out.WriteByte('"')
}

// invalid wraps a decoder error without its message, which can quote input bytes.
func invalid(err error) error {
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: unexpected end", ErrInvalidJSON)
	}
	if se, ok := errors.AsType[*json.SyntaxError](err); ok {
		return fmt.Errorf("%w: syntax error at offset %d", ErrInvalidJSON, se.Offset)
	}
	return ErrInvalidJSON
}

// allowed reports whether s may pass unhashed at n, or under key when n is nil.
func (e *Enforcer) allowed(key string, n *node, s string) bool {
	if n != nil {
		return n.allows(s)
	}
	if set, ok := e.enums[key]; ok {
		_, ok := set[s]
		return ok
	}
	switch {
	case strings.HasSuffix(key, "_at"):
		return isUTCTimestamp(s)
	case strings.HasSuffix(key, "_on"):
		return isDate(s)
	}
	return false
}

// noteOffEnum records an enum whose set lacks s in Result.OffEnum: n's field path, or key when
// n is nil.
func (w *walker) noteOffEnum(key string, n *node, s string) {
	var name string
	switch {
	case n != nil:
		if !n.offEnum(s) {
			return
		}
		name = n.path
	default:
		set, ok := w.e.enums[key]
		if !ok {
			return
		}
		if _, in := set[s]; in {
			return
		}
		name = key
	}
	if !slices.Contains(w.offEnum, name) {
		w.offEnum = append(w.offEnum, name)
	}
}

// isUTCTimestamp accepts exactly what time.Time.MarshalJSON produces for a UTC time.
func isUTCTimestamp(s string) bool {
	if !strings.HasSuffix(s, "Z") {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	return err == nil && t.Location() == time.UTC && t.Format(time.RFC3339Nano) == s
}

func isDate(s string) bool {
	const layout = time.DateOnly
	t, err := time.Parse(layout, s)
	return err == nil && t.Format(layout) == s
}
