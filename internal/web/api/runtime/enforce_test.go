package runtime

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/testutil"
)

func TestEnforceHashesStrings(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	a.Router(false).Get("/item", a.Handle(func(*http.Request) (any, error) {
		return dto.Health{Status: sentinel + "x"}, nil
	}))
	w1 := testutil.Get(t, a.Router(false), "/item")
	w2 := testutil.Get(t, a.Router(false), "/item")
	require.Equal(t, http.StatusOK, w1.Code)
	assert.Equal(t, w1.Body.String(), w2.Body.String(), "deterministic within an epoch")
	assert.Equal(t, w1.Header().Get(HeaderFanworkEpoch), w2.Header().Get(HeaderFanworkEpoch))
	assert.Regexp(t, `^\{"status":"h_[0-9a-f]{24}"\}$`, w1.Body.String())
	assert.Equal(t, strconv.Itoa(w1.Body.Len()), w1.Header().Get("Content-Length"))

	// The same string hashes the same on the admin socket.
	a.Router(true).Get("/item", a.Handle(func(*http.Request) (any, error) {
		return dto.Health{Status: sentinel + "x"}, nil
	}))
	assert.Equal(t, w1.Body.String(), testutil.Get(t, a.Router(true), "/item").Body.String())
}

func TestEnforceUnknownKey(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	a.Router(false).Get("/bad", a.Handle(func(*http.Request) (any, error) {
		return map[string]string{sentinel + "key": sentinel + "value"}, nil
	}))
	for _, target := range []string{"/bad", "/bad" + revealQuery} {
		w := testutil.Get(t, a.Router(false), target)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, dto.TypeInternal, testutil.DecodeProblem(t, w).Type)
		testutil.AssertNoSentinel(t, w, logs, sentinel)
	}
	assert.Contains(t, logs.String(), "api response refused by enforcer")
}

func TestEnforceNonJSON(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	a.Router(false).Get("/text", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Location", "/elsewhere")
		_, _ = w.Write([]byte(sentinel + "text"))
	})
	w := testutil.Get(t, a.Router(false), "/text")
	require.Equal(t, http.StatusForbidden, w.Code)
	p := testutil.DecodeProblem(t, w)
	assert.Equal(t, dto.TypeRevealRequired, p.Type)
	assert.Equal(t, DetailNotJSON.Message(), p.Detail)
	assert.Empty(t, w.Header().Get("Location"), "a replaced response drops the handler's headers")
	testutil.AssertNoSentinel(t, w, logs, sentinel)

	w = testutil.Get(t, a.Router(false), "/text"+revealQuery)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, sentinel+"text", w.Body.String())
	assert.Equal(t, "text/plain", w.Header().Get("Content-Type"))
	assert.Equal(t, "revealed", w.Header().Get(HeaderFanwork))
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

func TestEnforceMissingContentType(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	a.Router(false).Get("/raw", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	w := testutil.Get(t, a.Router(false), "/raw")
	require.Equal(t, http.StatusForbidden, w.Code, "JSON without a JSON content type is not trusted")
	assert.Equal(t, dto.TypeRevealRequired, testutil.DecodeProblem(t, w).Type)
}

func TestEnforceInvalidJSON(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	a.Router(false).Get("/broken", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", ContentTypeJSON)
		_, _ = w.Write([]byte(`{"status":`))
	})
	w := testutil.Get(t, a.Router(false), "/broken")
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, dto.TypeInternal, testutil.DecodeProblem(t, w).Type)
}

// JSON written around respond has no Go type to check against: refused even when it would
// match a DTO.
func TestEnforceUntypedJSON(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	a.Router(false).Get("/raw", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", ContentTypeJSON)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	for _, target := range []string{"/raw", "/raw" + revealQuery} {
		w := testutil.Get(t, a.Router(false), target)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, dto.TypeInternal, testutil.DecodeProblem(t, w).Type)
	}
	assert.Contains(t, logs.String(), "not written by respond")
}

// A body that doesn't match its declared type (an unregistered type, here) is refused.
func TestEnforceUnregisteredType(t *testing.T) {
	type other struct {
		Status string `json:"status"`
	}
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	a.Router(false).Get("/other", a.Handle(func(*http.Request) (any, error) { return other{Status: "ok"}, nil }))
	w := testutil.Get(t, a.Router(false), "/other")
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, dto.TypeInternal, testutil.DecodeProblem(t, w).Type)
}

func TestEnforceEmptyBody(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	a.Router(false).Delete("/gone", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	w := testutil.Do(t, a.Router(false), httptest.NewRequest(http.MethodDelete, "/gone", nil))
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.Bytes())
	assert.Equal(t, "hashed", w.Header().Get(HeaderFanwork))
}

func TestEnforceResponseTooLarge(t *testing.T) {
	a, logs := testutil.NewAPI(t, testOptions, testNew, nil)
	a.Router(false).Get("/huge", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", ContentTypeJSON)
		chunk := bytes.Repeat([]byte("a"), 1<<20)
		for range MaxResponseBytes>>20 + 1 {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	})
	w := testutil.Get(t, a.Router(false), "/huge")
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, dto.TypeInternal, testutil.DecodeProblem(t, w).Type)
	assert.Contains(t, logs.String(), "response too large")
}

// Every problem the middleware chain produces is valid problem+json with registered values.
func TestMiddlewareProblemsAreWellFormed(t *testing.T) {
	a, _ := testutil.NewAPI(t, testOptions, testNew, nil)
	for _, target := range []string{"/nope", "/api/v1/healthz?x=1", "/api/v1/healthz?" + strings.Repeat("a", MaxURLBytes)} {
		w := testutil.Get(t, a.Public(), target)
		p := testutil.DecodeProblem(t, w)
		_, known := p.Type.Info()
		assert.True(t, known, p.Type)
		assert.True(t, p.Detail != "" || len(p.Errors) > 0, "every middleware problem explains itself")
	}
}
