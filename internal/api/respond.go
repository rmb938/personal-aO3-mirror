package api

import (
	"encoding/json/v2"
	"log/slog"
	"net/http"
	"strconv"
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

// handle adapts h: it caps the request body, calls h and writes its result with respond.
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
	if p.Code == string(CodeUnsupportedMediaType) && r.Method == methodQuery {
		w.Header().Set("Accept-Query", contentTypeJSON)
	}
	respond(w, r, log, p.Status, contentTypeProblem, p)
}

// respond is the single place that encodes a JSON response body.
func respond(w http.ResponseWriter, r *http.Request, log *slog.Logger, status int, contentType string, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		log.LogAttrs(r.Context(), slog.LevelError, "api encode error",
			slog.String("route", r.Pattern), slog.String("type", typeName(v)))
		p := errCode(CodeInternal).problem()
		body, _ = json.Marshal(p) // a Problem always marshals
		status, contentType = p.Status, contentTypeProblem
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// methodQuery is the QUERY method (RFC 10008).
const methodQuery = "QUERY"
