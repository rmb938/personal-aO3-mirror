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
	"syscall"

	"github.com/rmb938/personal-aO3-mirror/internal/fakeao3"
	"github.com/rmb938/personal-aO3-mirror/internal/scheduler"
)

// lockDaemon takes an exclusive lock so only one daemon runs per data dir.
func lockDaemon(dataDir string) (func(), error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dataDir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("another ao3mirror run is already using this data dir")
		}
		return nil, err
	}
	f.Truncate(0)
	fmt.Fprintf(f, "%d\n", os.Getpid())
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

func runDaemon(ctx context.Context, a *app, errOut io.Writer) int {
	unlock, err := lockDaemon(a.cfg.DataDir)
	if err != nil {
		fmt.Fprintf(errOut, "run: %v\n", err)
		return 1
	}
	defer unlock()
	s := scheduler.New(scheduler.Options{
		Store: a.store, Client: a.client, Logger: a.log,
		DataDir: a.cfg.DataDir, KeepVersions: a.cfg.Content.KeepVersions,
	})
	a.log.Info("daemon starting", "interval", a.cfg.Rate.Interval.String(), "daily_budget", a.cfg.Rate.DailyBudget,
		"user_agent_contact_set", a.cfg.Contact.Discord != "")
	if err := s.Run(ctx); err != nil {
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
	srv, err := fakeao3.NewOn(*addr)
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	return 0
}
