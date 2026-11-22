package runtime

import (
	"cmp"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
)

const (
	ContentTypeJSON    = "application/json"
	ContentTypeProblem = "application/problem+json"
	// WWWAuthenticate is every 401's challenge: a bearer access token (RFC 6750 §3).
	WWWAuthenticate = `Bearer realm="ao3mirror"`
)

// HandlerFunc is the shape of a handler: it gets the request and returns a value or an error.
// It never sees the http.ResponseWriter, so it can't write anything respond doesn't check.
type HandlerFunc func(r *http.Request) (any, error)

// Response lets a handler pick a non-200 status or a Location header.
type Response struct {
	Status   int
	Location string // a path built from IDs only
	Body     any
}

// noContent is a 204 response: no body, so nothing for the enforcer to check.
func NoContent() *Response { return &Response{Status: http.StatusNoContent} }

// Blob is a 200 response of raw bytes that are not JSON (an image), with their Content-Type.
// It is fanwork content: the enforcer passes it only revealed and replaces it with 403
// reveal-required in hashed mode, so a handler checks Revealed before reading it.
type Blob struct {
	ContentType string // the stored type (image/*); "" is sent as application/octet-stream
	Body        []byte
}

// BlobCSP is a Blob's Content-Security-Policy, in place of the API's default: an image opened
// directly (an SVG) can style itself but never load, embed or run anything.
const BlobCSP = "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; sandbox"

// writeBlob writes b. It never records a body type: the enforcer refuses a JSON Content-Type
// without one, so a Blob cannot pass as JSON.
func writeBlob(w http.ResponseWriter, b *Blob) {
	h := w.Header()
	h.Set("Content-Type", cmp.Or(b.ContentType, "application/octet-stream"))
	h.Set("Content-Length", strconv.Itoa(len(b.Body)))
	h.Set("Content-Security-Policy", BlobCSP)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b.Body)
}

// handle adapts h: it caps the request body, calls h and writes its result with respond (or,
// for a 204, the status alone; for a Blob, its bytes).
func Handle(log *slog.Logger, h HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		}
		v, err := h(r)
		if err != nil {
			respondError(w, r, log, err)
			return
		}
		if b, ok := v.(*Blob); ok {
			writeBlob(w, b)
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
		Respond(w, r, log, status, ContentTypeJSON, v)
	}
}

// respondError writes err as a problem. Unexpected errors are logged (route pattern only; the
// error text comes from our own code and holds IDs, not content).
func respondError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	p, internal := ProblemFor(err)
	if internal {
		log.LogAttrs(r.Context(), slog.LevelError, "api handler error",
			slog.String("route", r.Pattern), slog.String("err", err.Error()))
	}
	if p.Type == dto.TypeUnsupportedMediaType && r.Method == MethodQuery {
		w.Header().Set(HeaderAcceptQuery, ContentTypeJSON)
	}
	if ex, ok := errors.AsType[controllers.ExistsError](err); ok {
		w.Header().Set("Location", route.Path(route.Source, ex.ID)) // the source that already exists
	}
	be, bearer := errors.AsType[*bearerError](err)
	switch {
	case bearer:
		// RFC 6750 §3.1: a request that carried bearer credentials learns what was wrong.
		w.Header().Set("WWW-Authenticate", WWWAuthenticate+`, error="`+be.code+`"`)
	case p.Status == http.StatusUnauthorized:
		// RFC 9110 §15.5.2: a 401 always names the scheme that would be accepted.
		w.Header().Set("WWW-Authenticate", WWWAuthenticate)
	case errors.Is(err, auth.ErrHasherBusy):
		w.Header().Set("Retry-After", "1")
	}
	if rl, ok := errors.AsType[controllers.RateLimitedError](err); ok {
		w.Header().Set("Retry-After", strconv.FormatInt(retryAfterSeconds(rl.RetryAfter), 10))
	}
	Respond(w, r, log, p.Status, ContentTypeProblem, p)
}

// retryAfterSeconds is a Retry-After delay in whole seconds: rounded up, at least 1 (RFC 9110
// §10.2.3 takes a non-negative integer; 0 would invite an immediate retry).
func retryAfterSeconds(d time.Duration) int64 {
	return max(1, int64((d+time.Second-1)/time.Second))
}

// respond is the single place that encodes a JSON response body. It tells the enforcer which Go
// type the body is, so the body is checked against that type's schema.
func Respond(w http.ResponseWriter, r *http.Request, log *slog.Logger, status int, contentType string, v any) {
	typ := reflect.TypeOf(v)
	body, err := json.Marshal(v)
	if err != nil {
		log.LogAttrs(r.Context(), slog.LevelError, "api encode error",
			slog.String("route", r.Pattern), slog.String("type", typeName(v)))
		p := problem.New(dto.TypeInternal).Problem()
		body, _ = json.Marshal(p) // a Problem always marshals
		status, contentType, typ = p.Status, ContentTypeProblem, reflect.TypeOf(p)
	}
	setBodyType(w, typ)
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// MethodQuery is the QUERY method (RFC 10008).
const MethodQuery = "QUERY"

// HeaderAcceptQuery lists the media types a resource accepts in QUERY bodies (RFC 10008).
const HeaderAcceptQuery = "Accept-Query"

// AcceptQuery advertises QUERY support (Accept-Query: application/json) on every response of a
// resource that accepts it, whatever the method.
func AcceptQuery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeaderAcceptQuery, ContentTypeJSON)
		next.ServeHTTP(w, r)
	})
}
