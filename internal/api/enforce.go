package api

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"maps"
	"mime"
	"net/http"
	"strconv"

	"github.com/rmb938/personal-aO3-mirror/internal/api/redact"
)

const (
	// maxResponseBytes caps a buffered response (500 above it).
	maxResponseBytes = 32 << 20

	headerFanwork      = "X-Fanwork"
	headerFanworkEpoch = "X-Fanwork-Key-Epoch"
)

var errResponseTooLarge = errors.New("api: response too large")

// bufferWriter buffers a response (headers, status, body) so the enforcer can check it before
// anything reaches the client. It has its own header map: a replaced response drops the inner
// handler's headers.
type bufferWriter struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (b *bufferWriter) Header() http.Header { return b.header }

func (b *bufferWriter) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}

func (b *bufferWriter) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	if b.overflow || b.body.Len()+len(p) > maxResponseBytes {
		b.overflow = true
		return 0, errResponseTooLarge
	}
	return b.body.Write(p)
}

// enforce is the response enforcer middleware (docs/part2-api.md, "Fanwork protection"). Every
// response from the handlers below it is buffered, then:
//   - a JSON body (application/json or application/problem+json) goes through redact.Enforcer:
//     in hashed mode every string not provably safe is hashed, and an unregistered key or
//     invalid JSON gives a 500 problem;
//   - an empty body passes;
//   - any other body passes only in revealed mode; in hashed mode it is replaced with 403
//     reveal_required;
//   - a body over maxResponseBytes gives a 500 problem.
//
// Every response gets X-Fanwork (hashed|revealed); hashed JSON gets X-Fanwork-Key-Epoch, and
// revealed responses get Cache-Control: no-store. The mode is read after the handler returns,
// because the reveal middleware runs further in.
func (a *API) enforce(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bw := &bufferWriter{header: http.Header{}}
		next.ServeHTTP(bw, r)

		mode := redact.Hashed
		if state(r.Context()).reveal {
			mode = redact.Revealed
		}
		status := bw.status
		if status == 0 {
			status = http.StatusOK
		}
		if bw.overflow {
			a.enforceFailed(w, r, mode, errResponseTooLarge)
			return
		}
		body := bw.body.Bytes()
		switch {
		case len(body) == 0:
			a.write(w, mode, bw.header, status, nil, "")
		case isJSONResponse(bw.header.Get("Content-Type")):
			res, err := a.enf.Enforce(mode, body)
			if err != nil {
				a.enforceFailed(w, r, mode, err)
				return
			}
			a.write(w, mode, bw.header, status, res.Body, res.Epoch)
		case mode == redact.Revealed:
			a.write(w, mode, bw.header, status, body, "")
		default:
			a.writeProblem(w, mode, &Error{Code: CodeRevealRequired, Status: http.StatusForbidden})
		}
	})
}

// enforceFailed logs why a response was refused (the error carries no content: see
// redact.UnknownKeyError and redact.ErrInvalidJSON) and sends a 500 problem instead.
func (a *API) enforceFailed(w http.ResponseWriter, r *http.Request, mode redact.Mode, err error) {
	a.log.LogAttrs(r.Context(), slog.LevelError, "api response refused by enforcer",
		slog.String("req_id", state(r.Context()).id),
		slog.String("route", routePattern(r)),
		slog.String("err", err.Error()))
	a.writeProblem(w, mode, errCode(CodeInternal))
}

// writeProblem sends e's problem in place of the handler's response. Problems hold only
// registered enum values, so they pass the enforcer unchanged; they still go through it.
func (a *API) writeProblem(w http.ResponseWriter, mode redact.Mode, e *Error) {
	p := e.problem()
	body, err := json.Marshal(p)
	if err != nil {
		panic(err) // a Problem always marshals
	}
	res, err := a.enf.Enforce(mode, body)
	if err != nil {
		panic(err) // problems are registered DTOs: a failure here is a programming error
	}
	h := http.Header{"Content-Type": {contentTypeProblem}}
	a.write(w, mode, h, p.Status, res.Body, res.Epoch)
}

// write sends the final response: the given headers (on top of those the outer middleware
// set), the fanwork headers and the body.
func (a *API) write(w http.ResponseWriter, mode redact.Mode, inner http.Header, status int, body []byte, epoch string) {
	h := w.Header()
	maps.Copy(h, inner)
	h.Set(headerFanwork, mode.String())
	if epoch != "" {
		h.Set(headerFanworkEpoch, epoch)
	} else {
		h.Del(headerFanworkEpoch)
	}
	if mode == redact.Revealed {
		h.Set("Cache-Control", "no-store")
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if len(body) > 0 {
		_, _ = w.Write(body)
	}
}

// isJSONResponse reports whether a response Content-Type is JSON or problem+json.
func isJSONResponse(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	return err == nil && (mt == contentTypeJSON || mt == contentTypeProblem)
}
