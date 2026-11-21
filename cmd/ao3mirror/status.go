package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/apiclient"
)

// runStatus prints a content-free overview: daemon, pacer, works, queue, requests, sources,
// recent errors. Safe for Claude to read (see CLAUDE.md).
func runStatus(ctx context.Context, a *app, args []string, out, errOut io.Writer) int {
	if len(args) != 0 {
		fmt.Fprint(errOut, "usage: ao3mirror status\n")
		return 2
	}
	st, err := a.client.GetStatus(ctx)
	if err != nil {
		return fail(errOut, "status", err)
	}
	printDaemon(out, st.Daemon)

	w := st.Works
	fmt.Fprintln(out, "\n== works")
	fmt.Fprintf(out, "by status: %s\n", counts(w.ByStatus, func(c apiclient.StatusWorksByStatusItem) (string, int) {
		return string(c.Status), c.Count
	}))
	fmt.Fprintf(out, "fetched: %s, awaiting first fetch: %s, tags: %s, creators: %s, series: %s\n",
		comma(w.Fetched), comma(w.NeverFetched), comma(st.Tags), comma(st.Creators), comma(st.Series))
	if w.ParseFailures > 0 {
		fmt.Fprintf(out, "parse failures: %s (raw pages kept and debug skeletons saved; run reparse after a parser fix)\n", comma(w.ParseFailures))
	}
	fmt.Fprintf(out, "raw pages: %s (%s raw, %s stored)\n", comma(st.RawPages.Count), mb(st.RawPages.Bytes), mb(st.RawPages.ZstdBytes))
	if im := st.Images; len(im.ByStatus) > 0 {
		fmt.Fprintf(out, "images: %s (%s stored)\n", counts(im.ByStatus, func(c apiclient.StatusImagesByStatusItem) (string, int) {
			return string(c.Status), c.Count
		}), mb(im.Bytes))
	}
	if ti := st.TagInfo; ti.Total > 0 {
		types := make([]string, len(ti.Types))
		for i, t := range ti.Types {
			types[i] = string(t)
		}
		fmt.Fprintf(out, "tag info: %s/%s fetched, %s due (%s used by mirrored works)\n",
			comma(ti.Fetched), comma(ti.Total), comma(ti.Due), strings.Join(types, "/"))
	}

	// One line per job kind with its counts by state; the API sorts by kind, then state.
	fmt.Fprintln(out, "\n== queue")
	if len(st.Jobs) == 0 {
		fmt.Fprintln(out, "empty")
	}
	for i := 0; i < len(st.Jobs); {
		j := i
		for j < len(st.Jobs) && st.Jobs[j].Kind == st.Jobs[i].Kind {
			j++
		}
		fmt.Fprintf(out, "%-11s %s\n", st.Jobs[i].Kind, counts(st.Jobs[i:j], func(c apiclient.StatusJobsItem) (string, int) {
			return string(c.State), c.Count
		}))
		i = j
	}

	rq := st.Requests
	fmt.Fprintln(out, "\n== requests")
	fmt.Fprintf(out, "last 1h: %d, last 24h: %d, 24h by class: %s\n", rq.Last1h, rq.Last24h,
		counts(rq.ByClass24h, func(c apiclient.StatusRequestsByClass24hItem) (string, int) { return string(c.Class), c.Count }))

	fmt.Fprintln(out, "\n== sources")
	fmt.Fprintf(out, "by state: %s\n", counts(st.Sources, func(c apiclient.StatusSourcesItem) (string, int) {
		return string(c.State), c.Count
	}))
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tTARGET\tSTATE\tseen/fetched/total\tNEXT POLL\tERROR")
	err = eachSource(ctx, a, false, func(s apiclient.Source) {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d/%d/%s\t%s\t%s\n", s.ID, s.Kind, sourceTarget(s), s.State,
			s.MemberWorks, s.FetchedWorks, nilTotal(s.TotalWorks), when(s.NextPollAt), dash(s.LastError.Or("")))
	})
	tw.Flush()
	if err != nil {
		return fail(errOut, "status", err)
	}

	if ps := st.PartialSeries; len(ps) > 0 {
		fmt.Fprintln(out, "\n== partial series (not a source; add one to mirror all of it)")
		tw = tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "SERIES\tHAVE\tMISSING PARTS\tMISSING WORK IDS")
		for _, p := range ps {
			fmt.Fprintf(tw, "%d\t%d/%d\t%s\t%s\n", p.ID, p.Mirrored, p.WorksTotal, idList(p.MissingPositions, 10), idList(p.MissingWorkIds, 5))
		}
		tw.Flush()
	}

	fmt.Fprintln(out, "\n== recent request errors")
	if len(st.RequestErrors) == 0 {
		fmt.Fprintln(out, "none")
	}
	tw = tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, r := range st.RequestErrors {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\t%s\n", clock(r.RequestedAt), r.Status, r.Class, r.Kind, r.Path, r.Reason, "job "+optID(r.JobID))
	}
	tw.Flush()

	fmt.Fprintln(out, "\n== recent failed jobs")
	if len(st.FailedJobs) == 0 {
		fmt.Fprintln(out, "none")
	}
	tw = tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, j := range st.FailedJobs {
		fmt.Fprintf(tw, "job %d\t%s\t%d\t%s\t%s\t%s\n", j.ID, j.Kind, j.Target, "source "+optID(j.SourceID), when(j.FinishedAt),
			jobError(j.LastErrorCode, j.LastErrorDetail))
	}
	tw.Flush()
	return 0
}

func printDaemon(out io.Writer, d apiclient.Daemon) {
	fmt.Fprintln(out, "== daemon")
	hb, ok := d.HeartbeatAt.Get()
	if !ok {
		fmt.Fprintln(out, "never ran")
		return
	}
	state := "running"
	if !d.Running {
		state = "not running"
	}
	cur := "-"
	if id, ok := d.CurrentJob.Get(); ok {
		cur = strconv.Itoa(int(id))
	}
	fmt.Fprintf(out, "%s (pid %d, phase %s, current job %s, heartbeat %s ago)\n", state, d.Pid.Or(0), dash(string(d.Phase.Or(""))), cur,
		time.Since(time.Time(hb)).Round(time.Second))
	if d.WaitingUntilAt.Set && d.Running {
		fmt.Fprintf(out, "waiting for next request slot until %s (pacing/backoff)\n", localTime(d.WaitingUntilAt))
	}
	p := d.Pacer
	fmt.Fprintf(out, "pacer: interval %s, slow factor %.2f, consecutive failures %d, breaker open %t, backoff until %s\n",
		time.Duration(p.Interval), p.SlowFactor, p.ConsecFails, p.BreakerOpen, localTime(p.BackoffUntilAt))
	if p.DailyBudget != 0 {
		fmt.Fprintf(out, "daily budget: %d / %d requests today\n", p.DayCount, p.DailyBudget)
	}
}

const debugUsage = "usage: ao3mirror debug job ID [--names] | debug work ID [--names]\n"

// runDebug prints the trace of one job or one work, content-free unless --names.
func runDebug(ctx context.Context, a *app, args []string, out, errOut io.Writer) int {
	if len(args) == 0 || (args[0] != "job" && args[0] != "work") {
		fmt.Fprint(errOut, debugUsage)
		return 2
	}
	fs := flag.NewFlagSet("debug "+args[0], flag.ContinueOnError)
	fs.SetOutput(errOut)
	names := fs.Bool("names", false, "show real paths, reasons and errors")
	arg, ok := parseWithPositional(fs, args[1:])
	if !ok {
		fmt.Fprint(errOut, debugUsage)
		return 2
	}
	id, ok := parseID(errOut, arg)
	if !ok {
		return 2
	}
	switch args[0] {
	case "job":
		d, err := a.client.DebugJob(ctx, apiclient.DebugJobParams{ID: id}, reveal(*names))
		if err != nil {
			return fail(errOut, "debug job", err)
		}
		printJob(out, d.Job, d.DedupeKey)
		fmt.Fprintln(out, "\nevents:")
		for _, e := range d.Events {
			// The fields that were set, as stored.
			fields, err := setFields(&e, "occurred_at", "event")
			if err != nil {
				fmt.Fprintf(errOut, "debug job: %v\n", err)
				return 1
			}
			fmt.Fprintf(out, "  %s  %-7s %s\n", clock(e.OccurredAt), e.Event, fields)
		}
		printRequests(out, d.Requests)
	case "work":
		d, err := a.client.DebugWork(ctx, apiclient.DebugWorkParams{ID: id}, reveal(*names))
		if err != nil {
			return fail(errOut, "debug work", err)
		}
		printWork(out, d.Work)
		fmt.Fprintln(out, "\njobs:")
		for _, j := range d.Jobs {
			fmt.Fprintf(out, "  job %d %s %s class %d attempts %d/%d %s %s\n", j.ID, j.Kind, j.State, j.Priority, j.Attempts, j.MaxAttempts,
				when(j.FinishedAt), jobError(j.LastErrorCode, j.LastErrorDetail))
		}
		printRequests(out, d.Requests)
	}
	return 0
}

func printJob(out io.Writer, j apiclient.Job, dedupe string) {
	printFields(out, [][2]string{
		{"id", strconv.Itoa(int(j.ID))}, {"kind", string(j.Kind)}, {"target", strconv.Itoa(j.Target)}, {"source", optID(j.SourceID)},
		{"class", strconv.Itoa(j.Priority)}, {"state", string(j.State)}, {"attempts", fmt.Sprintf("%d/%d", j.Attempts, j.MaxAttempts)},
		{"not before", when(j.NotBeforeAt)}, {"created", when(j.CreatedAt)}, {"started", when(j.StartedAt)},
		{"finished", when(j.FinishedAt)}, {"last error", jobError(j.LastErrorCode, j.LastErrorDetail)}, {"dedupe", dedupe},
	})
}

func printRequests(out io.Writer, reqs []apiclient.Request) {
	fmt.Fprintln(out, "\nrequests:")
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, r := range reqs {
		fmt.Fprintf(tw, "  %s\t%s\t%d\t%s\t%s\t%s\t%dB\t%dms\twaited %s\t%s\n", clock(r.RequestedAt), r.ReqID,
			r.Status, r.Class, r.Kind, r.Path, r.Bytes, time.Duration(r.Duration).Milliseconds(), time.Duration(r.Waited).Round(time.Second), r.Reason)
	}
	tw.Flush()
}

// printWork shows numeric/status fields only: no title, summary, names or tags.
func printWork(out io.Writer, d apiclient.WorkDiag) {
	exp := "?"
	if n, ok := d.ChaptersExpected.Get(); ok {
		exp = strconv.Itoa(n)
	}
	printFields(out, [][2]string{
		{"work", strconv.Itoa(int(d.ID))}, {"status", string(d.Status)}, {"published", date(d.PublishedOn)},
		{"revised", date(d.RevisedOn)}, {"revised at last fetch", date(d.FetchedRevisedOn)}, {"words", comma(d.Words)},
		{"chapters", fmt.Sprintf("%d/%s", d.ChaptersPosted, exp)},
		{"kudos/hits/comments/bookmarks", fmt.Sprintf("%d/%d/%d/%d", d.Stats.Kudos, d.Stats.Hits, d.Stats.Comments, d.Stats.Bookmarks)},
		{"restricted/anonymous", fmt.Sprintf("%t/%t", d.Restricted, d.Anonymous)},
		{"tags/creators/chapter rows/sources", fmt.Sprintf("%d/%d/%d/%d", d.Rows.Tags, d.Rows.Creators, d.Rows.Chapters, d.Rows.Sources)},
		{"images", strconv.Itoa(d.Rows.Images)},
		{"raw versions", fmt.Sprintf("%d (%s)", d.Rows.RawPages, mb(d.Rows.RawBytes))}, {"chapter bodies", strconv.Itoa(d.Rows.ChapterBodies)},
		{"parser version", strconv.Itoa(d.ParserVersion)},
		{"first seen", when(d.FirstSeenAt)}, {"last listed", when(d.LastListedAt)},
		{"last fetched", when(d.LastFetchedAt)}, {"last checked", when(d.LastCheckedAt)},
		{"fetch failed", when(d.FetchFailedAt)},
	})
}

// optID is an optional id (a job's source, a request's job), or "-" when it has none.
func optID(id apiclient.OptID) string {
	if v, ok := id.Get(); ok {
		return strconv.Itoa(int(v))
	}
	return "-"
}
