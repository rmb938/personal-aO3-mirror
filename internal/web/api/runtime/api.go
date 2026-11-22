package runtime

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/redact"
)

// Options configures the API. `serve` fills it from the `api:` config section.
type Options struct {
	// Version is the build version reported by /api/v1/version.
	Version string
	// NoReveal makes the server refuse the reveal param (403 reveal_disabled).
	NoReveal bool
	// TrustedProxies are the CIDR prefixes whose X-Forwarded-For is trusted (public listener).
	TrustedProxies []string
	// AllowedHosts are the host names (no port, any case) the public listener answers to; any
	// other Host gets 421 misdirected-request. The admin socket does not check Host.
	AllowedHosts []string
	// RequestTimeout is the per-request context deadline.
	RequestTimeout time.Duration
	// HashKeyRotation is the hash key lifetime (0: one key per process; else >= 1h).
	HashKeyRotation time.Duration
	// GeneralPerMin is the general per-client rate limit on the public listener. The admin
	// socket allows AdminRateFactor times as many.
	GeneralPerMin int
	// SourceAddPerHour is the per-client limit on POST /api/v1/sources (public listener; the
	// admin socket allows AdminRateFactor times as many), on top of the general limit.
	SourceAddPerHour int
	// SearchPerMin is the per-client limit on the QUERY search routes together (public
	// listener; the admin socket allows AdminRateFactor times as many), on top of the general
	// limit.
	SearchPerMin int
	// SignupPerHour is the per-client limit on the signup and reset routes together, both steps
	// (the admin socket allows AdminRateFactor times as many), on top of the general limit.
	SignupPerHour int
	// LoginPerMin is the per-client limit on each login step (password, then second factor;
	// each has its own budget), and RefreshPerMin on the refresh route. The admin socket allows
	// AdminRateFactor times as many; both are on top of the general limit.
	LoginPerMin   int
	RefreshPerMin int
	// Now is the hasher's clock (nil: time.Now). Tests use it to drive key rotation.
	Now func() time.Time
}

type RouteBuilder func(*API, *controllers.Controllers, *chi.Mux, bool)

// AdminRateFactor scales the public rate limits for the admin socket (every limit group).
const AdminRateFactor = 10

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
func New(opts Options, log *slog.Logger, controllers *controllers.Controllers, registerRoutes RouteBuilder) (*API, error) {
	if log == nil {
		return nil, errors.New("api: nil logger")
	}
	if controllers == nil {
		return nil, errors.New("api: nil controllers")
	}
	if registerRoutes == nil {
		return nil, errors.New("api: nil registerRoutes")
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
	if len(opts.AllowedHosts) == 0 {
		return nil, errors.New("api: no allowed hosts")
	}
	for _, h := range opts.AllowedHosts {
		if !config.ValidHost(h) {
			return nil, fmt.Errorf("api: allowed host %q is not a host name or IP address", h)
		}
	}
	if opts.RequestTimeout <= 0 {
		return nil, errors.New("api: request timeout must be positive")
	}
	if opts.GeneralPerMin <= 0 || opts.SourceAddPerHour <= 0 || opts.SearchPerMin <= 0 || opts.SignupPerHour <= 0 ||
		opts.LoginPerMin <= 0 || opts.RefreshPerMin <= 0 {
		return nil, errors.New("api: rate limits must be positive")
	}
	h, err := redact.NewHasher(opts.HashKeyRotation, opts.Now)
	if err != nil {
		return nil, err
	}
	enf, err := NewEnforcer(h, opts.Version)
	if err != nil {
		return nil, err
	}
	a := &API{opts: opts, log: log, ctl: controllers, enf: enf}
	a.public = a.router(false, registerRoutes)
	a.admin = a.router(true, registerRoutes)
	return a, nil
}

// Public is the handler for the public TCP listener (the one the Cloudflare Tunnel exposes).
func (a *API) Public() http.Handler { return a.public }

// Admin is the handler for the admin unix socket: every public route plus the admin routes.
func (a *API) Admin() http.Handler { return a.admin }

// Router is one listener's router as a *chi.Mux, for tests that walk or extend the routes.
func (a *API) Router(admin bool) *chi.Mux {
	if admin {
		return a.admin
	}
	return a.public
}

func (a *API) Version() string { return a.opts.Version }

func (a *API) SourceAddRateLimit(listener string, admin bool) func(http.Handler) http.Handler {
	n := a.opts.SourceAddPerHour
	if admin {
		n *= AdminRateFactor
	}
	return a.rateLimit(n, time.Hour, listener)
}

// SearchRateLimit is the search limit (SearchPerMin a minute). Build it once per listener and
// put it on every QUERY search route, so they share one budget.
func (a *API) SearchRateLimit(listener string, admin bool) func(http.Handler) http.Handler {
	n := a.opts.SearchPerMin
	if admin {
		n *= AdminRateFactor
	}
	return a.rateLimit(n, time.Minute, listener)
}

// SignupRateLimit is the signup and reset limit (SignupPerHour an hour). Build it once per
// listener and put it on all four routes, so they share one budget.
func (a *API) SignupRateLimit(listener string, admin bool) func(http.Handler) http.Handler {
	n := a.opts.SignupPerHour
	if admin {
		n *= AdminRateFactor
	}
	return a.rateLimit(n, time.Hour, listener)
}

// LoginRateLimit is a login step's limit (LoginPerMin a minute). Each call is a new budget:
// build one per step and listener.
func (a *API) LoginRateLimit(listener string, admin bool) func(http.Handler) http.Handler {
	return a.perMinute(a.opts.LoginPerMin, listener, admin)
}

// RefreshRateLimit is the refresh route's limit (RefreshPerMin a minute).
func (a *API) RefreshRateLimit(listener string, admin bool) func(http.Handler) http.Handler {
	return a.perMinute(a.opts.RefreshPerMin, listener, admin)
}

func (a *API) perMinute(n int, listener string, admin bool) func(http.Handler) http.Handler {
	if admin {
		n *= AdminRateFactor
	}
	return a.rateLimit(n, time.Minute, listener)
}

// router builds one listener's router with the full middleware chain (docs/part2-api.md, "HTTP
// hardening"; the order matters).
func (a *API) router(admin bool, registerRoutes RouteBuilder) *chi.Mux {
	listener, rate := "public", a.opts.GeneralPerMin
	if admin {
		listener, rate = "admin", rate*AdminRateFactor
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
	)
	if !admin {
		// A unix socket can't be reached by DNS rebinding, and clients name it freely.
		r.Use(a.hostCheck(a.opts.AllowedHosts))
	}
	r.Use(
		a.urlLimits,
		a.rateLimit(rate, time.Minute, listener),
		a.throttle(maxInFlight, maxBacklog, backlogWait),
		timeout(a.opts.RequestTimeout),
		a.crossOrigin(),
		a.reveal,
	)
	r.NotFound(a.Handle(func(*http.Request) (any, error) { return nil, problem.WithDetail(dto.TypeNotFound, DetailNoRoute) }))
	r.MethodNotAllowed(a.methodNotAllowed)
	registerRoutes(a, a.ctl, r, admin)
	return r
}

// Handle wraps a HandlerFunc to adapt it to http.HandlerFunc, capping the request body and
// handling the response/error.
func (a *API) Handle(h HandlerFunc) http.HandlerFunc { return Handle(a.log, h) }

// routedMethods are the methods the API routes; 405 responses list those a path supports.
var routedMethods = []string{
	http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, MethodQuery,
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
	respondError(w, r, a.log, problem.WithDetail(dto.TypeMethodNotAllowed, DetailMethodNotAllowed))
}
