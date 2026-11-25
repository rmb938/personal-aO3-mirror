package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/e2eseed"
	"github.com/rmb938/personal-aO3-mirror/internal/logx"
)

const uiServerUsage = `usage: ao3mirror dev ui-server [--addr 127.0.0.1:0] [--log-level warn]

Serves the API and the embedded web UI over a new temporary data dir seeded with synthetic
works and a test account, for the web UI's Playwright tests (docs/part4-ui.md stage 4).
Reveal is on: everything in it is lorem ipsum. It never opens an existing data dir.

Prints one JSON line on stdout once listening: the URL, the account's credentials and the
seeded IDs. Stops on Ctrl+C, SIGTERM or when stdin closes, then deletes the directory.
`

// uiServerInfo is the line ui-server prints once it is listening.
type uiServerInfo struct {
	URL string `json:"url"`
	e2eseed.Seeded
}

// runUIServer runs `dev ui-server`. Playwright starts it (web/tests/global-setup.ts) and closes
// its stdin to stop it, so a test run that dies never leaves it running.
func runUIServer(args []string, stdin io.Reader, out, errOut io.Writer) int {
	fl := flag.NewFlagSet("dev ui-server", flag.ContinueOnError)
	fl.SetOutput(errOut)
	fl.Usage = func() { fmt.Fprint(errOut, uiServerUsage) }
	addr := fl.String("addr", "127.0.0.1:0", "listen address (loopback; port 0 picks a free one)")
	level := fl.String("log-level", "warn", "log level (debug|info|warn|error), to stderr")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if fl.NArg() > 0 {
		fl.Usage()
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, stdin)
		cancel()
	}()
	if err := uiServer(ctx, *addr, *level, out, errOut); err != nil {
		fmt.Fprintf(errOut, "ui-server: %v\n", err)
		return 1
	}
	return 0
}

// uiServer seeds a new temp data dir, serves it until ctx ends, then deletes it.
func uiServer(ctx context.Context, addr, level string, out, errOut io.Writer) (err error) {
	log, closer, err := logx.New(logx.Options{Level: level, Stderr: errOut})
	if err != nil {
		return err
	}
	defer closer.Close()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("--addr %s: the host must be a loopback IP", addr)
	}
	dir, err := os.MkdirTemp("", "ao3mirror-ui-e2e-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()

	var lc net.ListenConfig
	tcp, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	url := "http://" + tcp.Addr().String()
	cfg := uiServerConfig(dir, url)
	seeded, err := e2eseed.Seed(ctx, e2eseed.Dirs{Data: cfg.DataDir, Images: cfg.Path(imageDir),
		Accounts: filepath.Dir(cfg.Path(cfg.API.AdminSocket))}, log) // where openAuth looks
	if err != nil {
		return errors.Join(err, tcp.Close())
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	s, err := newServerOn(ctx, cfg, log, false, tcp)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- s.serve(ctx) }()
	line, err := json.Marshal(uiServerInfo{URL: url, Seeded: seeded})
	if err == nil {
		_, err = fmt.Fprintf(out, "%s\n", line)
	}
	if err != nil {
		stop() // nobody learns the URL: shut down at once
	}
	return errors.Join(err, <-done)
}

// uiServerConfig is the defaults over dir, with url as api.public_url and rate limits high
// enough that a whole test run from one IP never meets them.
func uiServerConfig(dir, url string) config.Config {
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.Contact.Discord = "@fixture_user" // never used: serve builds no AO3 client
	cfg.API.PublicURL = url
	cfg.API.Rate.GeneralPerMin = 100_000
	cfg.API.Rate.UIPerMin = 100_000
	return cfg
}
