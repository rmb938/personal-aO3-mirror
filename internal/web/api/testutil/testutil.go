// Package testutil provides shared helpers for API tests.
package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func NewAPI[O, A any](t *testing.T, options func(*testing.T) O, newAPI func(*testing.T, O, *slog.Logger) (*A, error), mutate func(*O)) (*A, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	opts := options(t)
	if mutate != nil {
		mutate(&opts)
	}
	a, err := newAPI(t, opts, log)
	require.NoError(t, err)
	return a, logs
}

func LogLines(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.Lines(logs.String()) {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m))
		out = append(out, m)
	}
	return out
}

func AccessLine(t *testing.T, logs *bytes.Buffer) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, line := range LogLines(t, logs) {
		if line["msg"] == "api request" {
			found = append(found, line)
		}
	}
	require.Len(t, found, 1)
	return found[0]
}

func Do(t *testing.T, h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func Get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	return Do(t, h, httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil))
}

func DecodeProblem(t *testing.T, w *httptest.ResponseRecorder) dto.Problem {
	t.Helper()
	assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
	var p dto.Problem
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	assert.Equal(t, w.Code, p.Status)
	return p
}

func RequireParamProblem(t *testing.T, w *httptest.ResponseRecorder, param dto.ParamName, msg string) {
	t.Helper()
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, msg)
	p := DecodeProblem(t, w)
	require.Equal(t, dto.TypeValidation, p.Type, msg)
	require.Len(t, p.Errors, 1, msg)
	assert.Equal(t, param, p.Errors[0].Parameter, msg)
	assert.NotEmpty(t, p.Errors[0].Detail, msg)
}

func AssertNoSentinel(t *testing.T, w *httptest.ResponseRecorder, logs *bytes.Buffer, sentinel string) {
	t.Helper()
	assert.NotContains(t, w.Body.String(), sentinel, "body")
	for key, values := range w.Header() {
		for _, value := range values {
			assert.NotContains(t, value, sentinel, "header %s", key)
		}
	}
	assert.NotContains(t, logs.String(), sentinel, "logs")
}
