package runtime

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
)

const (
	sentinel    = "SENTINEL-"
	revealQuery = "?" + RevealParam + "=1"
)

func logLines(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.Lines(logs.String()) {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m))
		out = append(out, m)
	}
	return out
}

func accessLine(t *testing.T, logs *bytes.Buffer) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, line := range logLines(t, logs) {
		if line["msg"] == "api request" {
			found = append(found, line)
		}
	}
	require.Len(t, found, 1)
	return found[0]
}

func newTestAPI(t *testing.T, mutate func(*Options)) (*API, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	st, err := store.Open(t.Context(), t.TempDir(), store.Options{Logger: log})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	cfg := config.Default()
	opts := Options{
		Logger: log, Controllers: controllers.New(controllers.Options{Store: st, Logger: log,
			Sources: cfg.Sources, Interval: cfg.Rate.Interval}),
		Version: "test", TrustedProxies: []string{"127.0.0.1/32", "::1/128"},
		RequestTimeout: 10 * time.Second, GeneralPerMin: 1000, SourceAddPerHour: 1000,
	}
	if mutate != nil {
		mutate(&opts)
	}
	// The runtime tests only need the health route for middleware behavior.
	opts.RegisterRoutes = func(a *API, r *chi.Mux, _ bool) error {
		r.Get(route.Healthz, a.Handle(func(req *http.Request) (any, error) {
			q, err := ParseParams(req)
			if err != nil {
				return nil, err
			}
			if err := q.Err(); err != nil {
				return nil, err
			}
			return dto.Health{Status: dto.HealthOK}, nil
		}))
		return nil
	}
	a, err := New(opts)
	require.NoError(t, err)
	return a, logs
}

func do(t *testing.T, h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, h, httptest.NewRequest(http.MethodGet, target, nil))
}

func decodeProblem(t *testing.T, w *httptest.ResponseRecorder) dto.Problem {
	t.Helper()
	assert.Equal(t, ContentTypeProblem, w.Header().Get("Content-Type"))
	var p dto.Problem
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	assert.Equal(t, w.Code, p.Status)
	return p
}

func requireParamProblem(t *testing.T, w *httptest.ResponseRecorder, param dto.ParamName, msg string) {
	t.Helper()
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, msg)
	p := decodeProblem(t, w)
	require.Equal(t, dto.TypeValidation, p.Type, msg)
	require.Len(t, p.Errors, 1, msg)
	assert.Equal(t, param, p.Errors[0].Parameter, msg)
	assert.NotEmpty(t, p.Errors[0].Detail, msg)
}

func assertNoSentinel(t *testing.T, w *httptest.ResponseRecorder, logs *bytes.Buffer) {
	t.Helper()
	assert.NotContains(t, w.Body.String(), sentinel)
	for key, values := range w.Header() {
		for _, value := range values {
			assert.NotContains(t, value, sentinel, "header %s", key)
		}
	}
	assert.NotContains(t, logs.String(), sentinel)
}
