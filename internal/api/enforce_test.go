package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
)

func TestEnforceHashesStrings(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	a.public.Get("/item", a.handle(func(*http.Request) (any, error) {
		return dto.Health{Status: sentinel + "x"}, nil
	}))
	w1 := get(a.public, "/item")
	w2 := get(a.public, "/item")
	require.Equal(t, http.StatusOK, w1.Code)
	assert.Equal(t, w1.Body.String(), w2.Body.String(), "deterministic within an epoch")
	assert.Equal(t, w1.Header().Get(headerFanworkEpoch), w2.Header().Get(headerFanworkEpoch))
	assert.Regexp(t, `^\{"status":"h_[0-9a-f]{24}"\}$`, w1.Body.String())
	assert.Equal(t, strconv.Itoa(w1.Body.Len()), w1.Header().Get("Content-Length"))

	// The same string hashes the same on the admin socket.
	a.admin.Get("/item", a.handle(func(*http.Request) (any, error) {
		return dto.Health{Status: sentinel + "x"}, nil
	}))
	assert.Equal(t, w1.Body.String(), get(a.admin, "/item").Body.String())
}

func TestEnforceUnknownKey(t *testing.T) {
	a, logs := newTestAPI(t, nil)
	a.public.Get("/bad", a.handle(func(*http.Request) (any, error) {
		return map[string]string{sentinel + "key": sentinel + "value"}, nil
	}))
	for _, target := range []string{"/bad", "/bad" + revealQuery} {
		w := get(a.public, target)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, dto.CodeInternal, decodeProblem(t, w).Code)
		assertNoSentinel(t, w, logs)
	}
	assert.Contains(t, logs.String(), "api response refused by enforcer")
}

func TestEnforceNonJSON(t *testing.T) {
	a, logs := newTestAPI(t, nil)
	a.public.Get("/text", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Location", "/elsewhere")
		_, _ = w.Write([]byte(sentinel + "text"))
	})
	w := get(a.public, "/text")
	require.Equal(t, http.StatusForbidden, w.Code)
	p := decodeProblem(t, w)
	assert.Equal(t, dto.CodeRevealRequired, p.Code)
	assert.Empty(t, w.Header().Get("Location"), "a replaced response drops the handler's headers")
	assertNoSentinel(t, w, logs)

	w = get(a.public, "/text"+revealQuery)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, sentinel+"text", w.Body.String())
	assert.Equal(t, "text/plain", w.Header().Get("Content-Type"))
	assert.Equal(t, "revealed", w.Header().Get(headerFanwork))
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

func TestEnforceMissingContentType(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	a.public.Get("/raw", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	w := get(a.public, "/raw")
	require.Equal(t, http.StatusForbidden, w.Code, "JSON without a JSON content type is not trusted")
	assert.Equal(t, dto.CodeRevealRequired, decodeProblem(t, w).Code)
}

func TestEnforceInvalidJSON(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	a.public.Get("/broken", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentTypeJSON)
		_, _ = w.Write([]byte(`{"status":`))
	})
	w := get(a.public, "/broken")
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, dto.CodeInternal, decodeProblem(t, w).Code)
}

func TestEnforceEmptyBody(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	a.public.Delete("/gone", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	w := do(a.public, httptest.NewRequest(http.MethodDelete, "/gone", nil))
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.Bytes())
	assert.Equal(t, "hashed", w.Header().Get(headerFanwork))
}

func TestEnforceResponseTooLarge(t *testing.T) {
	a, logs := newTestAPI(t, nil)
	a.public.Get("/huge", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentTypeJSON)
		chunk := bytes.Repeat([]byte("a"), 1<<20)
		for range maxResponseBytes>>20 + 1 {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	})
	w := get(a.public, "/huge")
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, dto.CodeInternal, decodeProblem(t, w).Code)
	assert.Contains(t, logs.String(), "response too large")
}

// Every problem the middleware chain produces is valid problem+json with registered values.
func TestMiddlewareProblemsAreWellFormed(t *testing.T) {
	a, _ := newTestAPI(t, nil)
	for _, target := range []string{"/nope", "/api/v1/healthz?x=1", "/api/v1/healthz?" + strings.Repeat("a", maxURLBytes)} {
		w := get(a.Public(), target)
		var p map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
		_, known := dto.ProblemCode(p["code"].(string)).Info()
		assert.True(t, known, p["code"])
	}
}
