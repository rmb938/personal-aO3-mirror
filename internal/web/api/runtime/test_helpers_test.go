package runtime

import (
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
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

// testHosts holds httptest.NewRequest's default Host.
var testHosts = []string{"example.com"}

func testOptions(t *testing.T) Options {
	t.Helper()
	return Options{
		Version: "test", TrustedProxies: []string{"127.0.0.1/32", "::1/128"}, AllowedHosts: testHosts,
		RequestTimeout: 10 * time.Second, GeneralPerMin: 1000, SourceAddPerHour: 1000, SearchPerMin: 1000,
		SignupPerHour: 1000,
	}
}

func testNew(t *testing.T, opts Options, log *slog.Logger) (*API, error) {
	t.Helper()

	cfg := config.Default()
	st, err := store.Open(t.Context(), t.TempDir(), store.Options{Logger: log})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	ctrls := controllers.New(controllers.Options{Store: st, Logger: log,
		Sources: cfg.Sources, Interval: cfg.Rate.Interval})

	return New(opts, log, ctrls, func(a *API, _ *controllers.Controllers, r *chi.Mux, _ bool) {
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
	})
}
