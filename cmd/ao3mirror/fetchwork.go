package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/jobs"
	"github.com/rmb938/personal-aO3-mirror/internal/reparse"
	"github.com/rmb938/personal-aO3-mirror/internal/scheduler"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// runFetchWork queues one work at the highest priority, or fetches it right away with --now.
func runFetchWork(ctx context.Context, a *app, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("fetch-work", flag.ContinueOnError)
	fs.SetOutput(errOut)
	now := fs.Bool("now", false, "fetch immediately in this process (only when no daemon is running)")
	arg, ok := parseWithPositional(fs, args)
	if !ok {
		fmt.Fprint(errOut, "usage: ao3mirror fetch-work [--now] <work id | AO3 work URL>\n")
		return 2
	}
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		src, perr := ao3url.Parse(arg)
		if perr != nil || src.Kind != ao3url.KindWork {
			fmt.Fprintf(errOut, "fetch-work: need a work id or /works/<id> URL\n")
			return 2
		}
		id, _ = strconv.ParseInt(src.Target, 10, 64)
	}
	jobID, created, err := a.store.EnqueueJob(ctx, store.Job{Kind: jobs.KindWork, Target: strconv.FormatInt(id, 10),
		Priority: jobs.ClassOnDemand, DedupeKey: jobs.WorkKey(id)})
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
	unlock, err := lockDaemon(a.cfg.DataDir)
	if err != nil {
		fmt.Fprintf(errOut, "fetch-work --now: %v; job %d stays queued for the daemon\n", err, jobID)
		return 1
	}
	defer unlock()
	s := scheduler.New(a.schedulerOptions())
	j, err := s.RunJob(ctx, jobID)
	if err != nil {
		fmt.Fprintf(errOut, "fetch-work --now: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "work %d: job %d %s %s\n", id, jobID, j.State, dash(j.LastError))
	if j.State != store.JobDone {
		return 1
	}
	return 0
}

// runReparse rebuilds parsed data from stored raw pages. It makes no network requests.
func runReparse(ctx context.Context, st *store.Store, a appCfg, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("reparse", flag.ContinueOnError)
	fs.SetOutput(errOut)
	all := fs.Bool("all", false, "reparse every stored work, not only those parsed by an older parser version")
	work := fs.Int64("work", 0, "reparse only this work id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	res, err := reparse.Run(ctx, st, reparse.Options{
		All: *all, WorkID: *work, DataDir: a.dataDir, Logger: a.log,
		Progress: func(r reparse.Result) {
			fmt.Fprintf(errOut, "... seen %d, reparsed %d, skipped %d, failed %d\n", r.Seen, r.Reparsed, r.Skipped, r.Failed)
		},
	})
	if err != nil {
		fmt.Fprintf(errOut, "reparse: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "reparse done (0 requests): seen %d, reparsed %d, skipped %d (current parser), failed %d\n",
		res.Seen, res.Reparsed, res.Skipped, res.Failed)
	if res.Failed > 0 {
		fmt.Fprintln(out, "failed pages were saved as skeletons under data/debug/ (see README \"Parser skeletons\")")
		return 1
	}
	return 0
}
