package api

import (
	"encoding/json/v2"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

// decodeJSON strictly decodes r's body into dst (a pointer to a DTO):
//   - Content-Type must be application/json (charset, if given, utf-8), else 415;
//   - the body is capped at maxBodyBytes by handle (413 above it);
//   - exactly one JSON value: no trailing data, no unknown fields, no duplicate keys, valid
//     UTF-8 (encoding/json/v2 defaults plus RejectUnknownMembers); else 400 invalid_body.
//
// Errors never quote the body. Callers still validate each field (ranges, checkText).
func decodeJSON(r *http.Request, dst any) error {
	if !isJSONContentType(r.Header.Values("Content-Type")) {
		return errCode(CodeUnsupportedMediaType)
	}
	if r.Body == nil {
		return errCode(CodeInvalidBody)
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return errCode(CodeBodyTooLarge)
		}
		return errCode(CodeInvalidBody)
	}
	if err := json.Unmarshal(raw, dst, json.RejectUnknownMembers(true)); err != nil {
		return errCode(CodeInvalidBody)
	}
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
