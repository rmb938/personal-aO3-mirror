package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/apiclient"
)

// runJobs lists jobs. Content-free: targets are numeric work/tag/series ids or page numbers.
func runJobs(ctx context.Context, a *app, args []string, out, errOut io.Writer) int {
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

	listJobParams := apiclient.ListJobsParams{}

	if len(*state) > 0 {
		listJobParams.State = apiclient.NewOptJobState(apiclient.JobState(*state))
	}

	if len(*kind) > 0 {
		listJobParams.Kind = apiclient.NewOptJobKind(apiclient.JobKind(*kind))
	}

	if *source != 0 {
		listJobParams.SourceID = apiclient.NewOptID(apiclient.ID(*source))
	}

	// TODO: do offset as well
	if *limit != 0 {
		listJobParams.PerPage = apiclient.NewOptInt(*limit)
	}

	jobsList, err := a.apiClient.ListJobs(ctx, listJobParams)
	if err != nil {
		fmt.Fprintf(errOut, "jobs: %v\n", err)
		return 1
	}

	if len(jobsList.Items) == 0 {
		fmt.Fprintln(out, "no jobs")
		return 0
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tTARGET\tSOURCE\tCLASS\tSTATE\tATTEMPTS\tNOT BEFORE\tLAST ACTIVITY\tERROR")
	for _, j := range jobsList.Items {
		src := "-"
		if j.SourceID.Set {
			src = fmt.Sprint(j.SourceID.Value)
		}
		last := time.Time(j.CreatedAt.Value)
		for _, t := range []time.Time{time.Time(j.StartedAt.Value), time.Time(j.FinishedAt.Value)} {
			if t.After(last) {
				last = t
			}
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d\t%s\t%d/%d\t%s\t%s\t%s\n", j.ID, j.Kind, j.Target, src, j.Priority, j.State,
			j.Attempts, j.MaxAttempts, when(time.Time(j.NotBeforeAt.Value)), when(last), dash(j.LastErrorDetail.Value))
	}
	tw.Flush()
	if len(jobsList.Items) == *limit {
		fmt.Fprintf(out, "(first %d; use --limit for more)\n", *limit)
	}
	return 0
}
