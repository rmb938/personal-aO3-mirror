package api

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

type testItem struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func serve(t *testing.T, log *slog.Logger, method string, h handlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	handle(log, h)(w, httptest.NewRequest(method, "/x", nil))
	return w
}

func TestHandleValue(t *testing.T) {
	w := serve(t, discardLogger(), http.MethodGet, func(*http.Request) (any, error) {
		return testItem{ID: 1, Name: "n"}, nil
	})
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"id":1,"name":"n"}`, w.Body.String())
	assert.Equal(t, strconv.Itoa(w.Body.Len()), w.Header().Get("Content-Length"))
}

func TestHandleResponse(t *testing.T) {
	w := serve(t, discardLogger(), http.MethodPost, func(*http.Request) (any, error) {
		return &Response{Status: http.StatusAccepted, Location: "/api/v1/source-adds/7", Body: testItem{ID: 7}}, nil
	})
	assert.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, "/api/v1/source-adds/7", w.Header().Get("Location"))
	assert.JSONEq(t, `{"id":7,"name":""}`, w.Body.String())
}

func TestHandleError(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))

	w := serve(t, log, http.MethodGet, func(*http.Request) (any, error) { return nil, store.ErrNoWork })
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, contentTypeProblem, w.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"code":"not_found","title":"Not found","status":404}`, w.Body.String())
	assert.Empty(t, logs.String(), "expected errors are not logged")

	w = serve(t, log, http.MethodGet, func(*http.Request) (any, error) { return nil, errField(CodeInvalidParam, "page") })
	assert.JSONEq(t, `{"code":"invalid_param","title":"Invalid parameter","status":400,"field":"page"}`, w.Body.String())

	w = serve(t, log, http.MethodGet, func(*http.Request) (any, error) { return nil, errors.New("db exploded") })
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.JSONEq(t, `{"code":"internal","title":"Internal server error","status":500}`, w.Body.String())
	assert.NotContains(t, w.Body.String(), "exploded", "error text never reaches the client")
	assert.Contains(t, logs.String(), "api handler error")
}

func TestHandleQuery415AdvertisesAcceptQuery(t *testing.T) {
	h := func(*http.Request) (any, error) { return nil, errCode(CodeUnsupportedMediaType) }
	w := serve(t, discardLogger(), methodQuery, h)
	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Accept-Query"))

	w = serve(t, discardLogger(), http.MethodPost, h)
	assert.Empty(t, w.Header().Get("Accept-Query"))
}

func TestHandleEncodeError(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	w := serve(t, log, http.MethodGet, func(*http.Request) (any, error) { return make(chan int), nil })
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, contentTypeProblem, w.Header().Get("Content-Type"))
	assert.Contains(t, logs.String(), "chan int")
}

func TestHandleCapsBody(t *testing.T) {
	var n int64
	h := func(r *http.Request) (any, error) {
		var err error
		n, err = io.Copy(io.Discard, r.Body)
		return nil, err
	}
	w := httptest.NewRecorder()
	handle(discardLogger(), h)(w, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(strings.Repeat("a", 2*maxBodyBytes))))
	assert.Equal(t, int64(maxBodyBytes), n)
	require.Equal(t, http.StatusInternalServerError, w.Code) // a raw read error is unexpected
}
