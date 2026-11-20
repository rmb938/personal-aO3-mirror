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
	// Revealed checks the structure (keys and shapes) but returns strings as they are. Only a
	// request with the reveal param gets it.
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

// UnknownKeyError reports an object key that the struct at that position doesn't declare. It
// carries only the key's hash, never the key.
type UnknownKeyError struct {
	KeyHash string
}

func (e *UnknownKeyError) Error() string {
	return "redact: unregistered JSON key " + e.KeyHash
}

// SchemaError reports a JSON value whose shape doesn't match the Go type it was enforced as
// (e.g. an object where the field is a string). It names only the Go field, never content.
type SchemaError struct {
	Path string
}

func (e *SchemaError) Error() string {
	return "redact: JSON does not match " + e.Path
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
	// OffEnum lists, once each and in order of first sight, the enum fields (Go field paths,
	// e.g. "dto.Health.Status") that held a value outside their set, and the pointer fields
	// that held something other than a pointer into their request types (hashed in Hashed
	// mode, like any string). It is a programming error (a model value missing from its DTO
	// type, a pointer built wrong) for the caller to log: paths are code, never content.
	OffEnum []string
}

// Enforcer rewrites JSON response bodies according to a registry snapshot. It is safe for
// concurrent use.
type Enforcer struct {
	hasher *Hasher
	// schemas are the compiled registered types.
	schemas map[reflect.Type]*node
}

// NewEnforcer snapshots reg (later registrations don't affect it) and compiles each registered
// type into a schema. Every RegisterFieldEnum and RegisterPointerField field must exist in a
// registered type.
func NewEnforcer(reg *Registry, h *Hasher) (*Enforcer, error) {
	if h == nil {
		return nil, errors.New("redact: NewEnforcer: nil hasher")
	}
	e := &Enforcer{hasher: h, schemas: make(map[reflect.Type]*node, len(reg.roots))}
	fieldEnums := make(map[fieldEnumKey]map[string]struct{}, len(reg.fieldEnums))
	for fk, set := range reg.fieldEnums {
		fieldEnums[fk] = maps.Clone(set)
	}
	pointerFields := make(map[fieldEnumKey][]reflect.Type, len(reg.pointerFields))
	for fk, types := range reg.pointerFields {
		pointerFields[fk] = slices.Clone(types)
	}
	c := newCompiler(fieldEnums, pointerFields)
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
	for fk := range pointerFields {
		if !c.used[fk] {
			return nil, fmt.Errorf("redact: RegisterPointerField %s.%s: no such field in a registered type", fk.t, fk.key)
		}
	}
	return e, nil
}

// Enforce checks and rewrites in, one JSON value marshaled from t: a type registered through
// RegisterTypes, or a pointer to one (else an error). The walk follows t's schema:
//   - each object must be the struct at that position: a key it doesn't declare is an
//     *UnknownKeyError;
//   - a value of the wrong JSON kind is a *SchemaError; null is allowed anywhere;
//   - numbers and booleans pass through;
//   - in Hashed mode a string passes unhashed only when its own field allows it: a value of
//     the field's Enum type or RegisterFieldEnum set, a JSON Pointer into a RegisterPointerField
//     field's request types, or an RFC3339 UTC timestamp or a
//     YYYY-MM-DD date in a field whose key ends in "_at" or "_on" (strings in arrays use the
//     array field's rule). Two fields sharing a key (two `status` enums) each allow only their
//     own values. Every other string becomes "h_" + 24 hex.
//
// The whole body is hashed with one epoch.
func (e *Enforcer) Enforce(mode Mode, t reflect.Type, in []byte) (Result, error) {
	if mode != Hashed && mode != Revealed {
		return Result{}, fmt.Errorf("redact: unknown mode %d", int(mode))
	}
	n, ok := e.schemas[rootType(t)]
	if !ok {
		return Result{}, fmt.Errorf("redact: type %v is not registered", t)
	}
	ep := e.hasher.Current()
	w := walker{mode: mode, mac: ep.newMac()}
	w.out.Grow(len(in) + len(in)/4)
	dec := json.NewDecoder(bytes.NewReader(in))
	dec.UseNumber()
	if err := w.value(dec, n); err != nil {
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
	mode Mode
	mac  *mac
	out  bytes.Buffer
	// offEnum collects Result.OffEnum.
	offEnum []string
}

// value reads and writes one JSON value sitting at schema node n.
func (w *walker) value(dec *json.Decoder, n *node) error {
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
			return w.array(dec, n.elem)
		default:
			return fmt.Errorf("%w: unexpected %c", ErrInvalidJSON, rune(t))
		}
	case string:
		if err := n.want(kindString); err != nil {
			return err
		}
		if n.offEnum(t) && !slices.Contains(w.offEnum, n.path) {
			w.offEnum = append(w.offEnum, n.path)
		}
		if w.mode == Revealed || n.allows(t) {
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

// want checks that a JSON value of kind k may sit at n. An opaque node takes any scalar.
func (n *node) want(k nodeKind) error {
	if n.kind == k || (n.kind == kindOpaque && k != kindObject && k != kindArray) {
		return nil
	}
	return &SchemaError{Path: n.path}
}

// object walks an object at struct node n.
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
		child, ok := n.fields[k]
		if !ok {
			return &UnknownKeyError{KeyHash: w.mac.hash(k)}
		}
		if i > 0 {
			w.out.WriteByte(',')
		}
		w.writeRawString(k) // registered keys are snake_case: no escaping needed
		w.out.WriteByte(':')
		if err := w.value(dec, child); err != nil {
			return err
		}
	}
	return w.closing(dec, '}')
}

// array walks an array whose elements sit at elem.
func (w *walker) array(dec *json.Decoder, elem *node) error {
	w.out.WriteByte('[')
	for i := 0; dec.More(); i++ {
		if i > 0 {
			w.out.WriteByte(',')
		}
		if err := w.value(dec, elem); err != nil {
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
