package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/rmb938/personal-aO3-mirror/internal/api"
	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// HTTP server limits (docs/part2-api.md, "HTTP hardening").
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 60 * time.Second
	idleTimeout       = 120 * time.Second
	maxHeaderBytes    = 16 << 10
	shutdownTimeout   = 10 * time.Second

	// maxSocketPath is the usable length of sun_path on Linux (108 bytes with the NUL).
	maxSocketPath = 107
)

const serveUsage = "usage: ao3mirror serve [--no-reveal]\n"

// runServe runs the API on the public TCP listener and the admin unix socket until ctx ends.
// It never builds an httpx client: the internet-facing process cannot reach AO3.
func runServe(ctx context.Context, cfg config.Config, log *slog.Logger, args []string, errOut io.Writer) int {
	fl := flag.NewFlagSet("serve", flag.ContinueOnError)
	fl.SetOutput(errOut)
	fl.Usage = func() { fmt.Fprint(errOut, serveUsage) }
	noReveal := fl.Bool("no-reveal", false, "refuse the per-request reveal param (403 reveal_disabled)")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if fl.NArg() > 0 {
		fl.Usage()
		return 2
	}
	s, err := newServer(ctx, cfg, log, *noReveal)
	if err != nil {
		fmt.Fprintf(errOut, "serve: %v\n", err)
		return 1
	}
	if err := s.serve(ctx); err != nil {
		fmt.Fprintf(errOut, "serve: %v\n", err)
		return 1
	}
	return 0
}

// server is the API bound to its two listeners, not yet serving. It owns the store: serve
// closes it when it returns.
type server struct {
	log           *slog.Logger
	cfg           config.API
	noReveal      bool
	store         *store.Store
	tcp, sock     net.Listener
	public, admin *http.Server
}

// newServer opens the store, builds the API over offline controllers (no AO3 client) and binds
// both listeners. ctx is every request's base context, so a signal cancels in-flight requests
// while shutdown waits for them. On an error, everything opened so far is closed.
func newServer(ctx context.Context, cfg config.Config, log *slog.Logger, noReveal bool) (s *server, err error) {
	st, err := store.Open(ctx, cfg.DataDir, store.Options{Logger: log})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, st.Close())
		}
	}()
	a, err := api.New(api.Options{
		Logger:          log,
		Controllers:     newControllers(cfg, log, st, nil),
		Version:         version,
		NoReveal:        noReveal,
		TrustedProxies:  cfg.API.TrustedProxies,
		RequestTimeout:  cfg.API.RequestTimeout,
		HashKeyRotation: cfg.API.HashKeyRotation,
		GeneralPerMin:   cfg.API.Rate.GeneralPerMin,
	})
	if err != nil {
		return nil, err
	}
	var lc net.ListenConfig
	tcp, err := lc.Listen(ctx, "tcp", cfg.API.Listen)
	if err != nil {
		return nil, fmt.Errorf("public listener: %w", err)
	}
	sock, err := listenAdminSocket(ctx, cfg.Path(cfg.API.AdminSocket))
	if err != nil {
		return nil, errors.Join(fmt.Errorf("admin socket: %w", err), tcp.Close())
	}
	return &server{
		log: log, cfg: cfg.API, noReveal: noReveal, store: st, tcp: tcp, sock: sock,
		public: newHTTPServer(ctx, a.Public(), log),
		admin:  newHTTPServer(ctx, a.Admin(), log),
	}, nil
}

func newHTTPServer(ctx context.Context, h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		// net/http's own messages (accept errors, TLS/HTTP2 noise). Handler panics never reach
		// it: the API's recoverer logs them without the value.
		ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}

// listenAdminSocket binds the admin socket at path with mode 0600. Its directory must not be
// reachable by group or others, so there is no window before the chmod in which another user
// could connect. A stale socket left by a crash is replaced; a live one (another serve) is not.
func listenAdminSocket(ctx context.Context, path string) (net.Listener, error) {
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf("path %s is %d bytes, over the %d-byte unix socket limit", path, len(path), maxSocketPath)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	st, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("directory %s has mode %s; it must be private to this user (chmod 700)", dir, st.Mode().Perm())
	}
	if err := removeStaleSocket(ctx, path); err != nil {
		return nil, err
	}
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, errors.Join(err, l.Close())
	}
	return l, nil
}

// removeStaleSocket deletes a socket at path that nothing is listening on. Anything else at
// path (a live socket, a regular file) is an error and left alone.
func removeStaleSocket(ctx context.Context, path string) error {
	st, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode().Type() != fs.ModeSocket {
		return fmt.Errorf("%s exists and is not a socket", path)
	}
	d := net.Dialer{Timeout: time.Second}
	if c, err := d.DialContext(ctx, "unix", path); err == nil {
		return errors.Join(fmt.Errorf("%s is in use (is another `ao3mirror serve` running?)", path), c.Close())
	}
	return os.Remove(path)
}

// serve runs both listeners until ctx ends or one fails, then shuts both down gracefully and
// closes the store.
func (s *server) serve(ctx context.Context) (err error) {
	defer func() { err = errors.Join(err, s.store.Close()) }()
	attrs := []any{
		"listen", s.tcp.Addr().String(), "admin_socket", s.sock.Addr().String(),
		"trusted_proxies", s.cfg.TrustedProxies, "no_reveal", s.noReveal,
		"request_timeout", s.cfg.RequestTimeout.String(), "hash_key_rotation", s.cfg.HashKeyRotation.String(),
		"general_per_min", s.cfg.Rate.GeneralPerMin,
	}
	s.log.Info("api serving", attrs...)
	if ap, err := netip.ParseAddrPort(s.tcp.Addr().String()); err == nil && !ap.Addr().IsLoopback() {
		s.log.Warn("api public listener is not loopback; it has no auth yet", "listen", s.tcp.Addr().String())
	}

	bound := []struct {
		srv *http.Server
		ln  net.Listener
	}{{s.public, s.tcp}, {s.admin, s.sock}}

	// gctx ends on the signal or when any listener fails; either way every server shuts down.
	g, gctx := errgroup.WithContext(ctx)
	for _, b := range bound {
		g.Go(func() error {
			// Serve returns ErrServerClosed once Shutdown starts; that is the normal exit.
			if err := b.srv.Serve(b.ln); !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		})
	}
	g.Go(func() error {
		<-gctx.Done()
		s.log.Info("api shutting down")
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		var errs []error
		for _, b := range bound {
			if err := b.srv.Shutdown(sctx); err != nil {
				errs = append(errs, fmt.Errorf("shutdown: %w", err), b.srv.Close())
			}
		}
		return errors.Join(errs...)
	})
	return g.Wait()
}
