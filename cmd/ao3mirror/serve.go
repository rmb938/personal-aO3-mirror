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
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/rmb938/personal-aO3-mirror/internal/auth"
	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/dirs"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
	"github.com/rmb938/personal-aO3-mirror/internal/web/ui"
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
func runServe(ctx context.Context, cfg config.Config, data *dataDirs, log *slog.Logger, args []string, errOut io.Writer) int {
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
	s, err := newServer(ctx, cfg, data, log, *noReveal)
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

// server is the API bound to its two listeners, not yet serving. It owns the store, users.db
// and keys.db: serve closes them when it returns.
type server struct {
	log           *slog.Logger
	cfg           config.Config
	noReveal      bool
	store         *store.Store
	accounts      accounts
	tcp, sock     net.Listener
	public, admin *http.Server
}

// accounts are users.db, keys.db, the backup directory and the Auth controller over them.
type accounts struct {
	users   *store.Users
	keys    *store.Keys
	backups *os.Root
	master  *controllers.MasterKeys
	auth    *controllers.Auth
}

// Close closes users.db, keys.db and the backup directory.
func (a accounts) Close() error {
	return errors.Join(a.users.Close(), a.keys.Close(), a.backups.Close())
}

// newServer binds the public listener on api.listen, then builds the server on it
// (newServerOn).
func newServer(ctx context.Context, cfg config.Config, data *dataDirs, log *slog.Logger, noReveal bool) (*server, error) {
	var lc net.ListenConfig
	tcp, err := lc.Listen(ctx, "tcp", cfg.API.Listen)
	if err != nil {
		return nil, fmt.Errorf("public listener: %w", err)
	}
	return newServerOn(ctx, cfg, data, log, noReveal, tcp)
}

// newServerOn opens the store and the accounts (users.db and keys.db, in the admin socket's
// private directory), builds the API over offline controllers (no AO3 client) and binds the
// admin socket; tcp is the public listener. ctx is every request's base context, so a signal
// cancels in-flight requests while shutdown waits for them. On an error, everything opened so
// far (tcp included) is closed.
func newServerOn(ctx context.Context, cfg config.Config, data *dataDirs, log *slog.Logger, noReveal bool,
	tcp net.Listener) (s *server, err error) {
	defer func() {
		if err != nil {
			err = errors.Join(err, tcp.Close())
		}
	}()
	st, err := store.Open(ctx, data.root, store.Options{Logger: log})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, st.Close())
		}
	}()
	acc, err := openAuth(ctx, cfg, data, log)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, acc.Close())
		}
	}()
	ctlOpts := controllerOptions(cfg, data, log, st, nil)
	ctlOpts.Auth = acc.auth
	uiFiles, err := ui.New()
	if err != nil {
		return nil, err
	}
	if !uiFiles.Built {
		log.LogAttrs(ctx, slog.LevelWarn, "web UI not built: serving the placeholder page")
	}
	a, err := api.New(runtime.Options{
		Version:          version,
		NoReveal:         noReveal,
		TrustedProxies:   cfg.API.TrustedProxies,
		AllowedHosts:     cfg.API.AllowedHosts,
		RequestTimeout:   cfg.API.RequestTimeout,
		HashKeyRotation:  cfg.API.HashKeyRotation,
		GeneralPerMin:    cfg.API.Rate.GeneralPerMin,
		UIPerMin:         cfg.API.Rate.UIPerMin,
		SourceAddPerHour: cfg.API.Rate.SourceAddPerHour,
		SearchPerMin:     cfg.API.Rate.SearchPerMin,
		SignupPerHour:    cfg.API.Rate.SignupPerHour,
		LoginPerMin:      cfg.API.Rate.LoginPerMin,
		RefreshPerMin:    cfg.API.Rate.RefreshPerMin,
		UI:               uiFiles,
	}, log, controllers.New(ctlOpts))
	if err != nil {
		return nil, err
	}
	sock, err := listenAdminSocket(ctx, data.root, cfg.API.AdminSocket)
	if err != nil {
		return nil, fmt.Errorf("admin socket: %w", err)
	}
	return &server{
		log: log, cfg: cfg, noReveal: noReveal, store: st, accounts: acc, tcp: tcp, sock: sock,
		public: newHTTPServer(ctx, a.Public(), log),
		admin:  newHTTPServer(ctx, a.Admin(), log),
	}, nil
}

// openAuth opens users.db and keys.db in the admin socket's directory (private to this user,
// like the socket) and builds the MasterKeys and Auth controllers over them. keys.db gets its
// first master key on first start; an interrupted master key rotation is finished, and a
// users.db under a key keys.db lacks is refused. On an error, both databases are closed.
func openAuth(ctx context.Context, cfg config.Config, data *dataDirs, log *slog.Logger) (acc accounts, err error) {
	dir, err := dirs.PrivateSub(data.root, filepath.Dir(cfg.API.AdminSocket))
	if err != nil {
		return accounts{}, err
	}
	// users.db and keys.db are open by now (SQLite keeps them by path), so the root isn't kept.
	defer func() { err = errors.Join(err, dir.Close()) }()
	backups, err := dirs.PrivateSub(data.root, cfg.API.BackupDir)
	if err != nil {
		return accounts{}, fmt.Errorf("api.backup_dir: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, backups.Close())
		}
	}()
	keys, err := store.OpenKeys(ctx, dir, store.Options{Logger: log})
	if err != nil {
		return accounts{}, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, keys.Close())
		}
	}()
	users, err := store.OpenUsers(ctx, dir, store.Options{Logger: log})
	if err != nil {
		return accounts{}, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, users.Close())
		}
	}()
	master, err := controllers.NewMasterKeys(ctx, controllers.MasterKeysOptions{Keys: keys, Users: users,
		Logger: log, Now: time.Now, Rotation: cfg.Auth.MasterKeyRotation, BackupDir: backups})
	if err != nil {
		return accounts{}, err
	}
	keyring := master.Keyring()
	hasher, err := auth.NewHasher(keyring, auth.HasherOptions{})
	if err != nil {
		return accounts{}, err
	}
	ctl, err := controllers.NewAuth(ctx, controllers.AuthOptions{
		Users: users, MasterKeys: master, Hasher: hasher, Logger: log, Now: time.Now,
		PublicURL:     cfg.API.PublicURL,
		AccessTTL:     cfg.Auth.AccessTTL,
		RefreshTTL:    cfg.Auth.RefreshTTL,
		MaxSessionAge: cfg.Auth.MaxSessionAge,
		InviteTTL:     cfg.Auth.InviteTTL,
		TOTPIssuer:    cfg.Auth.TOTPIssuer,

		SigningKeyRotation: cfg.Auth.SigningKeyRotation,
	})
	if err != nil {
		return accounts{}, err
	}
	return accounts{users: users, keys: keys, backups: backups, master: master, auth: ctl}, nil
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

// listenAdminSocket binds the admin socket at rel (relative to data, api.admin_socket) with
// mode 0600. Its directory is created 0700 and must not be reachable by group or others, so
// there is no window before the chmod in which another user could connect. A stale socket left
// by a crash is replaced; a live one (another serve) is not. Every file operation goes through
// the directory's root; only bind and dial take the socket's path (its root's name joined with
// the socket's file name).
func listenAdminSocket(ctx context.Context, data *os.Root, rel string) (l net.Listener, err error) {
	dir, err := dirs.PrivateSub(data, filepath.Dir(rel))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	name := filepath.Base(rel)
	path := filepath.Join(dir.Name(), name)
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf("path %s is %d bytes, over the %d-byte unix socket limit", path, len(path), maxSocketPath)
	}
	if err := removeStaleSocket(ctx, dir, name, path); err != nil {
		return nil, err
	}
	var lc net.ListenConfig
	l, err = lc.Listen(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	if err := dir.Chmod(name, 0o600); err != nil {
		return nil, errors.Join(err, l.Close())
	}
	return l, nil
}

// removeStaleSocket deletes the socket name in dir (at path) when nothing is listening on it.
// Anything else there (a live socket, a regular file) is an error and left alone.
func removeStaleSocket(ctx context.Context, dir *os.Root, name, path string) error {
	st, err := dir.Lstat(name)
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
	return dir.Remove(name)
}

// serve runs both listeners until ctx ends or one fails, then shuts both down gracefully and
// closes the store.
func (s *server) serve(ctx context.Context) (err error) {
	defer func() { err = errors.Join(err, s.store.Close(), s.accounts.Close()) }()
	ac, au := s.cfg.API, s.cfg.Auth
	attrs := []any{
		"listen", s.tcp.Addr().String(), "admin_socket", s.sock.Addr().String(),
		"trusted_proxies", ac.TrustedProxies, "allowed_hosts", ac.AllowedHosts, "no_reveal", s.noReveal,
		"request_timeout", ac.RequestTimeout.String(), "hash_key_rotation", ac.HashKeyRotation.String(),
		"general_per_min", ac.Rate.GeneralPerMin, "ui_per_min", ac.Rate.UIPerMin, "search_per_min", ac.Rate.SearchPerMin,
		"source_add_per_hour", ac.Rate.SourceAddPerHour, "signup_per_hour", ac.Rate.SignupPerHour,
		"login_per_min", ac.Rate.LoginPerMin, "refresh_per_min", ac.Rate.RefreshPerMin,
		"public_url", ac.PublicURL, "access_ttl", au.AccessTTL.String(), "refresh_ttl", au.RefreshTTL.String(),
		"max_session_age", au.MaxSessionAge.String(), "invite_ttl", au.InviteTTL.String(),
		"signing_key_rotation", au.SigningKeyRotation.String(), "master_key_rotation", au.MasterKeyRotation.String(),
	}
	s.log.Info("api serving", attrs...)

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
		runKeyChecks(gctx, s.log, keyCheckInterval, keyChecks(s.accounts))
		return nil
	})
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

// keyCheckInterval is how often serve checks the keys' ages: a rotation or a retired master
// key's deletion only needs to land within an hour of its due time.
const keyCheckInterval = time.Hour

// keyCheck is one of serve's periodic key checks; name is a fixed label for its log line.
type keyCheck struct {
	name string
	run  func(context.Context) error
}

// keyChecks are serve's checks: the signing key rotation, the master key rotation and the
// retired master keys' deletion (docs/part3-key-rotation.md "Rotation").
func keyChecks(acc accounts) []keyCheck {
	return []keyCheck{
		{"signing key rotation", func(ctx context.Context) error {
			_, err := acc.auth.RotateSigningKeyIfDue(ctx)
			return err
		}},
		{"master key rotation", func(ctx context.Context) error {
			_, err := acc.master.RotateMasterKeyIfDue(ctx)
			return err
		}},
		{"retired master key deletion", func(ctx context.Context) error {
			_, err := acc.master.DeleteRetiredMasterKeys(ctx)
			return err
		}},
	}
}

// runKeyChecks runs every check straight away (a server that was down past a due date catches
// up at startup), then every tick until ctx ends. A failed check is logged and retried on the
// next tick.
func runKeyChecks(ctx context.Context, log *slog.Logger, every time.Duration, checks []keyCheck) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		for _, c := range checks {
			if err := c.run(ctx); err != nil && ctx.Err() == nil {
				log.Warn(c.name+" check failed", "err", err.Error())
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
