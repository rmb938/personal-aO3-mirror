package api

import (
	"encoding/json/v2"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/api/problem"
	"github.com/rmb938/personal-aO3-mirror/internal/api/request"
)

// decodeJSON strictly decodes r's body into dst (a pointer to a request type), then validates
// it, recording every invalid member on v:
//   - Content-Type must be application/json (charset, if given, utf-8), else 415;
//   - the body is capped at maxBodyBytes by handle (413 above it);
//   - exactly one JSON value: no trailing data, no unknown fields, no duplicate keys, valid
//     UTF-8 (encoding/json/v2 defaults plus RejectUnknownMembers); else 400 invalid-body.
//
// An unreadable body is returned at once (before any parameter error); validation problems
// are only recorded, so the caller returns them with its other ones (v.Err). Errors never
// quote the body.
func decodeJSON(r *http.Request, dst request.Request, v *problem.Validation) error {
	if !isJSONContentType(r.Header.Values("Content-Type")) {
		return problem.WithDetail(dto.TypeUnsupportedMediaType, detailMediaType)
	}
	if r.Body == nil {
		return problem.WithDetail(dto.TypeInvalidBody, detailInvalidBody)
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return problem.WithDetail(dto.TypeBodyTooLarge, detailBodyTooLarge)
		}
		return problem.WithDetail(dto.TypeInvalidBody, detailInvalidBody)
	}
	if err := json.Unmarshal(raw, dst, json.RejectUnknownMembers(true)); err != nil {
		return problem.WithDetail(dto.TypeInvalidBody, detailInvalidBody)
	}
	dst.Validate(v)
	return nil
}

// isJSONContentType reports whether the request has exactly one Content-Type of
// application/json, with no parameter other than charset=utf-8.
func isJSONContentType(vals []string) bool {
	if len(vals) != 1 {
		return false
	}
	mt, params, err := mime.ParseMediaType(vals[0])
	if err != nil || mt != contentTypeJSON {
		return false
	}
	for k, v := range params {
		if k != "charset" || !strings.EqualFold(v, "utf-8") {
			return false
		}
	}
	return true
}
