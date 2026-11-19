package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// runJobs lists jobs. Content-free: targets are numeric work/tag/series ids or page numbers.
func runJobs(ctx context.Context, ctl *controllers.Controllers, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("jobs", flag.ContinueOnError)
	fs.SetOutput(errOut)
	state := fs.String("state", "", "queued|running|done|failed|cancelled (default: all)")
	kind := fs.String("kind", "", "poll|backfill|work|tag_info|series_info (default: all)")
	source := fs.Int64("source", 0, "only jobs of this source id")
	limit := fs.Int("limit", 50, "max rows")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(errOut, "jobs: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if *limit <= 0 {
		*limit = 50
	}
	order := model.JobsNewest
	if *state == model.JobQueued {
		order = model.JobsOldest
	}
	js, err := ctl.FindJobs(ctx, model.JobFilter{State: *state, Kind: *kind, SourceID: *source, Limit: *limit, Order: order})
	if err != nil {
		fmt.Fprintf(errOut, "jobs: %v\n", err)
		return 1
	}
	if len(js) == 0 {
		fmt.Fprintln(out, "no jobs")
		return 0
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tTARGET\tSOURCE\tCLASS\tSTATE\tATTEMPTS\tNOT BEFORE\tLAST ACTIVITY\tERROR")
	for _, j := range js {
		src := "-"
		if j.SourceID > 0 {
			src = fmt.Sprint(j.SourceID)
		}
		last := j.CreatedAt
		for _, t := range []time.Time{j.StartedAt, j.FinishedAt} {
			if t.After(last) {
				last = t
			}
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d\t%s\t%d/%d\t%s\t%s\t%s\n", j.ID, j.Kind, j.Target, src, j.Priority, j.State,
			j.Attempts, j.MaxAttempts, when(j.NotBefore), when(last), dash(j.LastError))
	}
	tw.Flush()
	if len(js) == *limit {
		fmt.Fprintf(out, "(first %d; use --limit for more)\n", *limit)
	}
	return 0
}
