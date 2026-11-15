// Command ao3mirror is a polite, personal AO3 mirror. See docs/plan.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/logx"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "0.1.0-dev"

const usage = `usage: ao3mirror [--config FILE] [--log-level LEVEL] <command> [args]

commands:
  version            print version
  config check       validate config and print the User-Agent that will be sent
  run                run the scraper daemon               (milestone 5)
  source ...         add/list/pause/resume/cancel/remove/confirm sources (milestone 4)
  fetch-work ID      queue a single work on demand        (milestone 6)
  reparse            rebuild parsed data from stored raw pages, no requests (milestone 6)
  status             content-free queue/progress summary  (milestone 5)
  debug job|work ID  content-free trace for one job/work  (milestone 5)
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ao3mirror", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	cfgPath := fs.String("config", "ao3mirror.yaml", "config file")
	logLevel := fs.String("log-level", "", "override log.level (debug|info|warn|error)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return 2
	}
	if rest[0] == "version" {
		fmt.Fprintln(stdout, version)
		return 0
	}

	// Every other command needs a valid config (contact.discord is required).
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "config %s invalid:\n%v\n", *cfgPath, err)
		return 1
	}
	if *logLevel != "" {
		cfg.Log.Level = *logLevel
	}
	logger, closer, err := logx.New(logx.Options{
		Level:    cfg.Log.Level,
		File:     cfg.Path(cfg.Log.File),
		MaxBytes: int64(cfg.Log.MaxSizeMB) << 20,
		MaxFiles: cfg.Log.MaxFiles,
		Stderr:   stderr,
	})
	if err != nil {
		fmt.Fprintf(stderr, "logging: %v\n", err)
		return 1
	}
	defer closer.Close()
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	_ = ctx

	switch rest[0] {
	case "config":
		if len(rest) < 2 || rest[1] != "check" {
			fs.Usage()
			return 2
		}
		fmt.Fprintf(stdout, "config ok: %s\ndata_dir: %s\nuser-agent: %s\nrate.interval: %s (floor %s)\n",
			*cfgPath, cfg.DataDir, cfg.UserAgent(version), cfg.Rate.Interval, config.MinInterval)
		logger.Debug("config checked", "path", *cfgPath, "interval", cfg.Rate.Interval.String())
		return 0
	case "run", "source", "fetch-work", "reparse", "status", "debug":
		fmt.Fprintf(stderr, "%s: not implemented yet (see docs/plan.md milestones)\n", rest[0])
		return 1
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", rest[0])
		fs.Usage()
		return 2
	}
}
