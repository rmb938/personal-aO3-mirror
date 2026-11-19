package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/api/redact"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
)

// Options configures the API. `serve` fills it from the `api:` config section.
type Options struct {
	Logger *slog.Logger
	// Controllers serve every store-backed route. `serve` builds them with no AO3 client.
	Controllers *controllers.Controllers
	// Version is the build version reported by /api/v1/version.
	Version string
	// NoReveal makes the server refuse the reveal param (403 reveal_disabled).
	NoReveal bool
	// TrustedProxies are the CIDR prefixes whose X-Forwarded-For is trusted (public listener).
	TrustedProxies []string
	// RequestTimeout is the per-request context deadline.
	RequestTimeout time.Duration
	// HashKeyRotation is the hash key lifetime (0: one key per process; else >= 1h).
	HashKeyRotation time.Duration
	// GeneralPerMin is the general per-client rate limit on the public listener. The admin
	// socket allows adminRateFactor times as many.
	GeneralPerMin int
	// Now is the hasher's clock (nil: time.Now). Tests use it to drive key rotation.
	Now func() time.Time
}

// adminRateFactor scales the public rate limits for the admin socket.
const adminRateFactor = 10

// API holds the two routers (public TCP and admin socket) and the state they share: one hasher,
// so a string hashes the same on both listeners within an epoch.
type API struct {
	opts   Options
	log    *slog.Logger
	ctl    *controllers.Controllers
	enf    *redact.Enforcer
	public *chi.Mux
	admin  *chi.Mux
}

// New validates opts and builds both routers.
func New(opts Options) (*API, error) {
	if opts.Logger == nil {
		return nil, errors.New("api: nil logger")
	}
	if opts.Controllers == nil {
		return nil, errors.New("api: nil controllers")
	}
	if opts.Version == "" {
		return nil, errors.New("api: empty version")
	}
	if len(opts.TrustedProxies) == 0 {
		return nil, errors.New("api: no trusted proxies")
	}
	for _, p := range opts.TrustedProxies {
		if _, err := netip.ParsePrefix(p); err != nil {
			return nil, fmt.Errorf("api: trusted proxy %q: %w", p, err)
		}
	}
	if opts.RequestTimeout <= 0 {
		return nil, errors.New("api: request timeout must be positive")
	}
	if opts.GeneralPerMin <= 0 {
		return nil, errors.New("api: general rate limit must be positive")
	}
	h, err := redact.NewHasher(opts.HashKeyRotation, opts.Now)
	if err != nil {
		return nil, err
	}
	enf, err := newEnforcer(h, opts.Version)
	if err != nil {
		return nil, err
	}
	a := &API{opts: opts, log: opts.Logger, ctl: opts.Controllers, enf: enf}
	a.public = a.router(false)
	a.admin = a.router(true)
	return a, nil
}

// Public is the handler for the public TCP listener (the one Funnel exposes).
func (a *API) Public() http.Handler { return a.public }

// Admin is the handler for the admin unix socket: every public route plus the admin routes.
func (a *API) Admin() http.Handler { return a.admin }

// router builds one listener's router with the full middleware chain (docs/part2-api.md, "HTTP
// hardening"; the order matters).
func (a *API) router(admin bool) *chi.Mux {
	listener, rate := "public", a.opts.GeneralPerMin
	if admin {
		listener, rate = "admin", rate*adminRateFactor
	}
	r := chi.NewRouter()
	r.Use(requestID)
	if !admin {
		// The socket has no IP peer; never trust XFF there.
		r.Use(middleware.ClientIPFromXFF(a.opts.TrustedProxies...))
	}
	r.Use(
		a.accessLog(listener),
		a.recoverer,
		securityHeaders,
		a.enforce,
		a.urlLimits,
		a.rateLimit(rate, time.Minute, listener),
		a.throttle(maxInFlight, maxBacklog, backlogWait),
		timeout(a.opts.RequestTimeout),
		a.crossOrigin(),
		a.reveal,
	)
	r.NotFound(a.handle(func(*http.Request) (any, error) { return nil, errCode(dto.CodeNotFound) }))
	r.MethodNotAllowed(a.methodNotAllowed)

	r.Get("/api/v1/healthz", a.handle(a.healthz))
	r.Get("/api/v1/version", a.handle(a.version))
	r.Get("/api/v1/sources", a.handle(a.listSources))
	r.Get("/api/v1/sources/{id}", a.handle(a.getSource))
	r.Get("/api/v1/source-adds/{job_id}", a.handle(a.getSourceAdd))
	r.Get("/api/v1/jobs", a.handle(a.listJobs))
	r.Get("/api/v1/jobs/{id}", a.handle(a.getJob))

	if admin {
		r.Group(func(chi.Router) {
			// Admin-only routes (stage 5) go here; the public router never has them.
		})
	}
	return r
}

func (a *API) handle(h handlerFunc) http.HandlerFunc { return handle(a.log, h) }

// routedMethods are the methods the API routes; 405 responses list those a path supports.
var routedMethods = []string{
	http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, methodQuery,
}

// methodNotAllowed is the 405 problem, with Allow listing the path's methods.
func (a *API) methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.Routes != nil {
		for _, m := range routedMethods {
			if rctx.Routes.Match(chi.NewRouteContext(), m, r.URL.Path) {
				w.Header().Add("Allow", m)
			}
		}
	}
	respondError(w, r, a.log, errCode(dto.CodeMethodNotAllowed))
}
