package problem

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// Validation collects the invalid parts of one request, so a client sees all of them in one
// 422 validation-error instead of fixing them one round trip at a time. Query and path
// parameters are recorded by the api package's params; body members by the request type's
// validate function, which takes the handler's *Validation.
type Validation struct {
	items []Item
}

// Param records an invalid query or path parameter.
func (v *Validation) Param(name string, d Detail) {
	v.items = append(v.items, Item{Param: name, Detail: d})
}

// Member records an invalid body member at the JSON Pointer built from the json keys (and
// slice indices) leading to it, e.g. Member(d, "filters", "2", "tag_id") → /filters/2/tag_id.
// The enforcer checks each pointer against the registered request types, so a pointer that
// doesn't name a member of one is hashed.
func (v *Validation) Member(d Detail, path ...string) {
	v.items = append(v.items, Item{Pointer: pointer(path...), Detail: d})
}

// Len is the number of invalid parts recorded so far.
func (v *Validation) Len() int { return len(v.items) }

// Err is nil when nothing was invalid, else the validation-error listing every item.
func (v *Validation) Err() error {
	if len(v.items) == 0 {
		return nil
	}
	return &Error{Type: dto.TypeValidation, Detail: DetailInvalidRequest, Errors: v.items}
}

// pointer joins json keys (snake_case, so nothing needs RFC 6901 escaping) and indices into a
// JSON Pointer.
func pointer(path ...string) string {
	return "/" + strings.Join(path, "/")
}

// ValidText reports whether s is valid UTF-8 with no control characters and at most maxRunes
// runes. A string that fails it gets DetailText (or a caller's more specific detail).
func ValidText(s string, maxRunes int) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxRunes {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
