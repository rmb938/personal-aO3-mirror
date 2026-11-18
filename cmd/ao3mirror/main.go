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
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	buildversion "github.com/rmb938/personal-aO3-mirror/internal/version"
)

var version = buildversion.Version

const usage = `usage: ao3mirror [--config FILE] [--log-level LEVEL] <command> [args]

commands:
  version            print version
  config check       validate config and print the User-Agent that will be sent
  run                run the scraper daemon (Ctrl+C to stop; resumes where it left off)
  source ...         add/list/show/confirm/pause/resume/cancel/remove/refresh/set sources
                     ('ao3mirror source' for details)
  fetch-work [--now] ID|URL    queue one work at top priority (--now: fetch here if no daemon)
  reparse [--all] [--work ID]  rebuild parsed tables from stored raw pages; makes no requests
  status             content-free queue/progress summary
  jobs [--state S] [--kind K] [--source ID] [--limit N]
                     list jobs (ids, targets, states, errors); queued jobs in queue order
  debug job|work ID  content-free trace for one job/work
  serve [--no-reveal]  run the JSON API (api: config); strings are hashed unless a request
                     asks to reveal; --no-reveal refuses that too (docs/part2-api.md)
  dev fake-server    serve a synthetic AO3 on 127.0.0.1 for local testing
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
	if rest[0] == "dev" {
		return runDev(rest[1:], stdout, stderr) // synthetic local AO3; makes no outside requests
	}
	// Help needs no config.
	if rest[0] == "help" || rest[0] == "-h" || rest[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if rest[0] == "source" && (len(rest) == 1 || rest[1] == "help" || rest[1] == "-h" || rest[1] == "--help") {
		fmt.Fprint(stdout, sourceUsage)
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
	case "serve":
		return runServe(ctx, cfg, logger, rest[1:], stderr)
	case "reparse", "jobs":
		// Offline: opens the store only; no HTTP client is built.
		st, err := store.Open(ctx, cfg.DataDir, store.Options{Logger: logger})
		if err != nil {
			fmt.Fprintf(stderr, "open: %v\n", err)
			return 1
		}
		defer st.Close()
		ctl := newControllers(cfg, logger, st, nil)
		if rest[0] == "jobs" {
			return runJobs(ctx, ctl, rest[1:], stdout, stderr)
		}
		return runReparse(ctx, ctl, rest[1:], stdout, stderr)
	case "source", "run", "status", "debug", "fetch-work":
		a, err := openApp(ctx, cfg, logger)
		if err != nil {
			fmt.Fprintf(stderr, "open: %v\n", err)
			return 1
		}
		defer a.Close()
		switch rest[0] {
		case "source":
			return runSource(ctx, a, rest[1:], stdout, stderr)
		case "run":
			return runDaemon(ctx, a, stderr)
		case "status":
			return runStatus(ctx, a, stdout, stderr)
		case "fetch-work":
			return runFetchWork(ctx, a, rest[1:], stdout, stderr)
		default:
			return runDebug(ctx, a, rest[1:], stdout, stderr)
		}
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", rest[0])
		fs.Usage()
		return 2
	}
}
