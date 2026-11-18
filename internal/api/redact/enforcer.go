package redact

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
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
}

// Enforcer rewrites JSON response bodies according to a registry snapshot. It is safe for
// concurrent use.
type Enforcer struct {
	hasher *Hasher
	keys   map[string]struct{}
	enums  map[string]map[string]struct{}
}

// NewEnforcer snapshots reg (later registrations don't affect it) and checks that every enum
// key is a registered key.
func NewEnforcer(reg *Registry, h *Hasher) (*Enforcer, error) {
	if h == nil {
		return nil, errors.New("redact: NewEnforcer: nil hasher")
	}
	e := &Enforcer{hasher: h, keys: maps.Clone(reg.keys), enums: make(map[string]map[string]struct{}, len(reg.enums))}
	for k, set := range reg.enums {
		if _, ok := e.keys[k]; !ok {
			return nil, fmt.Errorf("redact: enum key %q is not a registered DTO key", k)
		}
		e.enums[k] = maps.Clone(set)
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
	if mode != Hashed && mode != Revealed {
		return Result{}, fmt.Errorf("redact: unknown mode %d", int(mode))
	}
	ep := e.hasher.Current()
	w := walker{e: e, mode: mode, mac: ep.newMac()}
	w.out.Grow(len(in) + len(in)/4)
	dec := json.NewDecoder(bytes.NewReader(in))
	dec.UseNumber()
	if err := w.value(dec, ""); err != nil {
		return Result{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Result{}, fmt.Errorf("%w: trailing data", ErrInvalidJSON)
	}
	res := Result{Body: w.out.Bytes()}
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
}

// value reads and writes one JSON value; key is the object key it sits under ("" at the top).
func (w *walker) value(dec *json.Decoder, key string) error {
	tok, err := dec.Token()
	if err != nil {
		return invalid(err)
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return w.object(dec)
		case '[':
			return w.array(dec, key)
		default:
			return fmt.Errorf("%w: unexpected %c", ErrInvalidJSON, rune(t))
		}
	case string:
		if w.mode == Revealed || w.e.allowed(key, t) {
			w.writeString(t)
		} else {
			w.writeRawString(w.mac.hash(t))
		}
	case json.Number:
		w.out.WriteString(string(t))
	case bool:
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

func (w *walker) object(dec *json.Decoder) error {
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
		if _, ok := w.e.keys[k]; !ok {
			return &UnknownKeyError{KeyHash: w.mac.hash(k)}
		}
		if i > 0 {
			w.out.WriteByte(',')
		}
		w.writeRawString(k) // registered keys are snake_case: no escaping needed
		w.out.WriteByte(':')
		if err := w.value(dec, k); err != nil {
			return err
		}
	}
	return w.closing(dec, '}')
}

func (w *walker) array(dec *json.Decoder, key string) error {
	w.out.WriteByte('[')
	for i := 0; dec.More(); i++ {
		if i > 0 {
			w.out.WriteByte(',')
		}
		if err := w.value(dec, key); err != nil {
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

// allowed reports whether s may pass unhashed under key.
func (e *Enforcer) allowed(key, s string) bool {
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
