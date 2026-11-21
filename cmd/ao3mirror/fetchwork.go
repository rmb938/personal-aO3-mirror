package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strconv"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/jobs"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/scheduler"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

const fetchWorkUsage = "usage: ao3mirror fetch-work [--now] <work id | AO3 work URL>\n"

// runFetchWork queues one work at the highest priority, or fetches it right away with --now. It
// is a direct command (docs/part2-api.md): it opens the store itself, and only --now, which
// needs the daemon lock, builds an AO3 client.
func runFetchWork(ctx context.Context, cfg config.Config, log *slog.Logger, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("fetch-work", flag.ContinueOnError)
	fs.SetOutput(errOut)
	now := fs.Bool("now", false, "fetch immediately in this process (only when no daemon is running)")
	arg, ok := parseWithPositional(fs, args)
	if !ok {
		fmt.Fprint(errOut, fetchWorkUsage)
		return 2
	}
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		src, perr := ao3url.Parse(arg)
		if perr != nil || src.Kind != ao3url.KindWork {
			fmt.Fprint(errOut, "fetch-work: need a work id or /works/<id> URL\n")
			return 2
		}
		id, _ = strconv.ParseInt(src.Target, 10, 64)
	}

	st, err := store.Open(ctx, cfg.DataDir, store.Options{Logger: log})
	if err != nil {
		fmt.Fprintf(errOut, "fetch-work: %v\n", err)
		return 1
	}
	defer st.Close()
	jobID, created, err := newControllers(cfg, log, st, nil).EnqueueWorkFetch(ctx, id, 0, jobs.ClassOnDemand)
	if err != nil {
		fmt.Fprintf(errOut, "fetch-work: %v\n", err)
		return 1
	}
	if !*now {
		verb := "queued"
		if !created {
			verb = "already queued (raised to on-demand priority)"
		}
		fmt.Fprintf(out, "work %d %s as job %d; the running daemon picks it up next (see: ao3mirror debug job %d)\n", id, verb, jobID, jobID)
		return 0
	}

	unlock, err := lockDaemon(cfg.DataDir)
	if err != nil {
		fmt.Fprintf(errOut, "fetch-work --now: %v; job %d stays queued for the daemon\n", err, jobID)
		return 1
	}
	defer func() {
		if err := unlock(); err != nil {
			fmt.Fprintf(errOut, "fetch-work --now: releasing lock: %v\n", err)
		}
	}()
	client, err := newAO3Client(cfg, log, st)
	if err != nil {
		fmt.Fprintf(errOut, "fetch-work --now: %v\n", err)
		return 1
	}
	s := scheduler.New(scheduler.Options{Store: st, Controllers: newControllers(cfg, log, st, client), Pacer: client, Logger: log})
	j, err := s.RunJob(ctx, jobID)
	if err != nil {
		fmt.Fprintf(errOut, "fetch-work --now: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "work %d: job %d %s %s\n", id, jobID, j.State, dash(j.LastError))
	if j.State != model.JobDone {
		return 1
	}
	return 0
}
