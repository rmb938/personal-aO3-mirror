package api

import (
	"strings"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
)

// validation collects the invalid parts of one request, so a client sees all of them in one
// 422 validation-error instead of fixing them one round trip at a time. Query and path
// parameters are recorded by params; body members by the request type's validate function
// (one per request DTO, beside its handler), which takes the handler's *validation.
type validation struct {
	items []ErrorItem
}

// param records an invalid query or path parameter.
func (v *validation) param(name string, d detail) {
	v.items = append(v.items, ErrorItem{Param: name, Detail: d})
}

// member records an invalid body member at the JSON Pointer built from the json keys (and
// slice indices) leading to it, e.g. member(d, "filters", "2", "tag_id") → /filters/2/tag_id.
// The enforcer checks each pointer against the registered request types, so a pointer that
// doesn't name a member of one is hashed.
func (v *validation) member(d detail, path ...string) {
	v.items = append(v.items, ErrorItem{Pointer: pointer(path...), Detail: d})
}

// err is nil when nothing was invalid, else the validation-error listing every item.
func (v *validation) err() error {
	if len(v.items) == 0 {
		return nil
	}
	return &Error{Type: dto.TypeValidation, Detail: detailInvalidRequest, Errors: v.items}
}

// pointer joins json keys (snake_case, so nothing needs RFC 6901 escaping) and indices into a
// JSON Pointer.
func pointer(path ...string) string {
	return "/" + strings.Join(path, "/")
}
