package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/rmb938/personal-aO3-mirror/internal/fakeao3"
	"github.com/rmb938/personal-aO3-mirror/internal/imagefetch"
)

var errDaemonLocked = errors.New("another ao3mirror run is already using this data dir")

// lockDaemon takes an exclusive lock so only one daemon runs per data dir. The returned unlock
// releases it and reports any error.
func lockDaemon(dataDir string) (unlock func() error, err error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dataDir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil { // not locked: don't leak the file
			err = errors.Join(err, f.Close())
		}
	}()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errDaemonLocked
		}
		return nil, err
	}
	// The pid in the lock file is informational (the flock is what matters).
	if err := f.Truncate(0); err != nil {
		return nil, fmt.Errorf("lock file: %w", err)
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		return nil, fmt.Errorf("lock file: %w", err)
	}
	return func() error {
		return errors.Join(syscall.Flock(int(f.Fd()), syscall.LOCK_UN), f.Close())
	}, nil
}

// daemonRunning reports whether a process (normally `ao3mirror run`) holds the daemon lock for
// dataDir. It takes no lock that lingers.
func daemonRunning(dataDir string) (bool, error) {
	unlock, err := lockDaemon(dataDir)
	if errors.Is(err, errDaemonLocked) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, unlock()
}

func runDaemon(ctx context.Context, a *app, errOut io.Writer) int {
	unlock, err := lockDaemon(a.cfg.DataDir)
	if err != nil {
		fmt.Fprintf(errOut, "run: %v\n", err)
		return 1
	}
	defer func() {
		if err := unlock(); err != nil {
			fmt.Fprintf(errOut, "run: releasing lock: %v\n", err)
		}
	}()
	s := a.scheduler()
	a.log.Info("daemon starting", "interval", a.cfg.Rate.Interval.String(), "daily_budget", a.cfg.Rate.DailyBudget,
		"user_agent_contact_set", a.cfg.Contact.Discord != "", "images", a.cfg.Images.Enabled,
		"tag_info_types", len(a.cfg.Tags.InfoTypes))

	// Image downloads go to external hosts (not AO3), so they run beside the AO3 loop with their
	// own per-host pacing.
	var wg sync.WaitGroup
	if a.cfg.Images.Enabled {
		w, err := imagefetch.New(imagefetch.Options{
			Store: a.store, UserAgent: a.client.UserAgent(), Dir: a.cfg.Path("images"),
			PerHostInterval: a.cfg.Images.PerHostInterval, MaxBytes: a.cfg.Images.MaxBytes, Logger: a.log,
		})
		if err != nil {
			fmt.Fprintf(errOut, "run: images: %v\n", err)
			return 1
		}
		wg.Go(func() {
			if err := w.Run(ctx); err != nil {
				a.log.Error("image worker stopped", "err", err.Error())
			}
		})
	}
	err = s.Run(ctx)
	wg.Wait()
	if err != nil {
		fmt.Fprintf(errOut, "run: %v\n", err)
		return 1
	}
	return 0
}

// runDev serves a synthetic AO3 for local end-to-end testing. Content is fake.
func runDev(args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] != "fake-server" {
		fmt.Fprint(errOut, "usage: ao3mirror dev fake-server [--addr 127.0.0.1:8099] [--works 45]\n")
		return 2
	}
	fs := flag.NewFlagSet("dev fake-server", flag.ContinueOnError)
	fs.SetOutput(errOut)
	addr := fs.String("addr", "127.0.0.1:8099", "listen address (loopback)")
	works := fs.Int("works", 45, "synthetic works in the fixture tag")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv, err := fakeao3.NewOn(ctx, *addr)
	if err != nil {
		fmt.Fprintf(errOut, "fake-server: %v\n", err)
		return 1
	}
	defer srv.Close()
	site := fakeao3.NewSite("Fixture Tag", 5000, *works)
	srv.SetSite(site)
	fmt.Fprintf(out, `fake AO3 (synthetic content) listening on %[1]s
in another shell, with a scratch data_dir in your config:
  export AO3MIRROR_BASE_URL=%[1]s
  ao3mirror --config <cfg> source add '%[2]s'
  ao3mirror --config <cfg> run
Ctrl+C to stop.
`, srv.URL, site.TagPath())
	<-ctx.Done()
	return 0
}
