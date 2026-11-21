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

// runJobs lists up to --limit jobs, newest first; a --state queued listing is in queue order
// (oldest first). Content-free: the API hashes job targets unless --names.
func runJobs(ctx context.Context, a *app, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("jobs", flag.ContinueOnError)
	fs.SetOutput(errOut)
	state := fs.String("state", "", "queued|running|done|failed|cancelled (default: all)")
	kind := fs.String("kind", "", "poll|backfill|work|tag_info|series_info|source_add (default: all)")
	source := fs.Int("source", 0, "only jobs of this source id")
	limit := fs.Int("limit", 50, "max rows")
	names := fs.Bool("names", false, "show real job targets and errors")
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
	p := apiclient.ListJobsParams{}
	if *state != "" {
		p.State.SetTo(apiclient.JobState(*state))
	}
	if *kind != "" {
		p.Kind.SetTo(apiclient.JobKind(*kind))
	}
	if *source != 0 {
		p.SourceID.SetTo(apiclient.ID(*source))
	}

	// Pages of up to listPerPage until --limit rows or the last job.
	var jobs []apiclient.Job
	total := 0
	per := min(*limit, listPerPage)
	p.PerPage.SetTo(per)
	for page := 1; len(jobs) < *limit; page++ {
		p.Page.SetTo(page)
		l, err := a.client.ListJobs(ctx, p, reveal(*names))
		if err != nil {
			return fail(errOut, "jobs", err)
		}
		jobs = append(jobs, l.Items...)
		total = l.Total
		if len(l.Items) < per || page*per >= l.Total {
			break
		}
	}
	jobs = jobs[:min(len(jobs), *limit)]

	if len(jobs) == 0 {
		fmt.Fprintln(out, "no jobs")
		return 0
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tTARGET\tSOURCE\tCLASS\tSTATE\tATTEMPTS\tNOT BEFORE\tLAST ACTIVITY\tERROR")
	for _, j := range jobs {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d\t%s\t%d/%d\t%s\t%s\t%s\n", j.ID, j.Kind, j.Target, optID(j.SourceID), j.Priority, j.State,
			j.Attempts, j.MaxAttempts, when(j.NotBeforeAt), whenTime(lastActivity(j)), jobError(j.LastErrorCode, j.LastErrorDetail))
	}
	tw.Flush()
	if total > len(jobs) {
		fmt.Fprintf(out, "(first %d of %s; use --limit for more)\n", len(jobs), comma(total))
	}
	return 0
}

// lastActivity is the latest of a job's created, started and finished times.
func lastActivity(j apiclient.Job) time.Time {
	var last time.Time
	for _, t := range []apiclient.OptTimestamp{j.CreatedAt, j.StartedAt, j.FinishedAt} {
		if v := time.Time(t.Value); t.Set && v.After(last) {
			last = v
		}
	}
	return last
}
