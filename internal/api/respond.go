package api

import (
	"encoding/json/v2"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/api/problem"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
)

const (
	contentTypeJSON    = "application/json"
	contentTypeProblem = "application/problem+json"
	// maxBodyBytes caps request bodies (http.MaxBytesReader).
	maxBodyBytes = 16 << 10
)

// handlerFunc is the only handler shape: it gets the request and returns a value or an error.
// It never sees the http.ResponseWriter, so it can't write anything respond doesn't check.
type handlerFunc func(r *http.Request) (any, error)

// Response lets a handler pick a non-200 status or a Location header.
type Response struct {
	Status   int
	Location string // a path built from IDs only
	Body     any
}

// noContent is a 204 response: no body, so nothing for the enforcer to check.
func noContent() *Response { return &Response{Status: http.StatusNoContent} }

// handle adapts h: it caps the request body, calls h and writes its result with respond (or,
// for a 204, the status alone).
func handle(log *slog.Logger, h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		v, err := h(r)
		if err != nil {
			respondError(w, r, log, err)
			return
		}
		status := http.StatusOK
		if res, ok := v.(*Response); ok {
			status = res.Status
			if res.Location != "" {
				w.Header().Set("Location", res.Location)
			}
			v = res.Body
			if status == http.StatusNoContent {
				w.WriteHeader(status)
				return
			}
		}
		respond(w, r, log, status, contentTypeJSON, v)
	}
}

// respondError writes err as a problem. Unexpected errors are logged (route pattern only; the
// error text comes from our own code and holds IDs, not content).
func respondError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	p, internal := problemFor(err)
	if internal {
		log.LogAttrs(r.Context(), slog.LevelError, "api handler error",
			slog.String("route", r.Pattern), slog.String("err", err.Error()))
	}
	if p.Type == dto.TypeUnsupportedMediaType && r.Method == methodQuery {
		w.Header().Set("Accept-Query", contentTypeJSON)
	}
	if ex, ok := errors.AsType[controllers.ExistsError](err); ok {
		w.Header().Set("Location", pathFor(routeSource, ex.ID)) // the source that already exists
	}
	respond(w, r, log, p.Status, contentTypeProblem, p)
}

// respond is the single place that encodes a JSON response body. It tells the enforcer which Go
// type the body is, so the body is checked against that type's schema.
func respond(w http.ResponseWriter, r *http.Request, log *slog.Logger, status int, contentType string, v any) {
	typ := reflect.TypeOf(v)
	body, err := json.Marshal(v)
	if err != nil {
		log.LogAttrs(r.Context(), slog.LevelError, "api encode error",
			slog.String("route", r.Pattern), slog.String("type", typeName(v)))
		p := problem.New(dto.TypeInternal).Problem()
		body, _ = json.Marshal(p) // a Problem always marshals
		status, contentType, typ = p.Status, contentTypeProblem, reflect.TypeOf(p)
	}
	setBodyType(w, typ)
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// methodQuery is the QUERY method (RFC 10008).
const methodQuery = "QUERY"
