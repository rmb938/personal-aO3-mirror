package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/apiclient"
)

// runStatus prints a content-free overview: daemon, pacer, works, queue, requests, sources,
// recent errors. Safe for Claude to read (see CLAUDE.md).
func runStatus(ctx context.Context, a *app, out, errOut io.Writer) int {
	resp, err := a.apiClient.GetStatus(ctx)
	if err != nil {
		fmt.Fprintf(errOut, "status: %v\n", err)
		return 1
	}
	c, daemon := resp.Works, resp.Daemon
	fmt.Fprintln(out, "== daemon")
	switch {
	case daemon.HeartbeatAt.IsSet():
		fmt.Fprintln(out, "never ran")
	default:
		state := "running"
		if !daemon.Running {
			state = "not running"
		}
		fmt.Fprintf(out, "%s (pid %d, phase %s, current job %d, heartbeat %s ago)\n", state, daemon.Pid.Value, daemon.Phase.Value, daemon.CurrentJob.Value,
			time.Since(time.Time(daemon.HeartbeatAt.Value)).Round(time.Second))
		if !daemon.WaitingUntilAt.IsSet() && daemon.Running {
			fmt.Fprintf(out, "waiting for next request slot until %s (pacing/backoff)\n", localTime(time.Time(daemon.WaitingUntilAt.Value)))
		}
		fmt.Fprintf(out, "pacer: interval %s, slow factor %.2f, consecutive failures %d, breaker open %t, backoff until %s\n",
			time.Duration(daemon.Pacer.Interval).String(), daemon.Pacer.SlowFactor, daemon.Pacer.ConsecFails, daemon.Pacer.BreakerOpen, dash(localTime(time.Time(daemon.Pacer.BackoffUntilAt.Value))))
		if daemon.Pacer.DailyBudget != 0 {
			fmt.Fprintf(out, "daily budget: %d / %d requests today\n", daemon.Pacer.DayCount, daemon.Pacer.DailyBudget)
		}
	}

	fmt.Fprintln(out, "\n== works")
	fmt.Fprintf(out, "by status: %#v\n", c.ByStatus)
	fmt.Fprintf(out, "fetched: %s, awaiting first fetch: %s, tags: %s, creators: %s, series: %s\n",
		comma(c.Fetched), comma(c.NeverFetched), comma(resp.Tags), comma(resp.Creators), comma(resp.Series))
	if c.ParseFailures > 0 {
		fmt.Fprintf(out, "parse failures: %s (raw pages kept and debug skeletons saved; run reparse after a parser fix)\n", comma(c.ParseFailures))
	}
	fmt.Fprintf(out, "raw pages: %s (%s raw, %s stored)\n", comma(resp.RawPages.Count), mb(int64(resp.RawPages.Bytes)), mb(int64(resp.RawPages.ZstdBytes)))
	if len(resp.Images.ByStatus) > 0 {
		fmt.Fprintf(out, "images: %#v (%s stored)\n", resp.Images.ByStatus, mb(int64(resp.Images.Bytes)))
	}

	if ti := resp.TagInfo; ti.Total > 0 {
		fmt.Fprintf(out, "tag info: %s/%s fetched, %s due (%s used by mirrored works)\n",
			comma(ti.Fetched), comma(ti.Total), comma(ti.Due), strings.Join(a.cfg.Tags.InfoTypes, "/"))
	}

	fmt.Fprintln(out, "\n== queue")
	jc := resp.Jobs
	kinds := make([]string, 0, len(jc))
	for _, k := range jc {
		kinds = append(kinds, string(k.Kind))
	}
	sort.Strings(kinds)
	if len(kinds) == 0 {
		fmt.Fprintln(out, "empty")
	}
	// TODO: idk what this is suppose to be
	// for _, k := range kinds {
	// 	fmt.Fprintf(out, "%-9s %s\n", k, kv(jc[k]))
	// }

	fmt.Fprintln(out, "\n== requests")
	// TODO: ??? how to do this?
	// fmt.Fprintf(out, "last 1h: %d, last 24h: %d, 24h by class: %s\n", c.Requests1h, c.Requests24h, kv(c.RequestsByClass))

	fmt.Fprintln(out, "\n== sources")
	fmt.Fprintf(out, "by state: %#v\n", c.ByStatus)
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tTARGET\tSTATE\tseen/fetched/total\tNEXT POLL\tERROR")
	// Paged like `source list`: a source added or removed meanwhile can shift a row across a
	// page boundary.
	const perPage = 100
	for offset := 0; ; offset += perPage {
		sources, err := a.apiClient.ListSources(ctx, apiclient.ListSourcesParams{
			Page:    apiclient.NewOptInt(offset),
			PerPage: apiclient.NewOptInt(perPage),
		})
		if err != nil {
			tw.Flush()
			fmt.Fprintf(errOut, "status: %v\n", err)
			return 1
		}
		for _, r := range sources.Items {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d/%d/%s\t%s\t%s\n", r.ID, r.Kind, r.Target, r.State,
				r.MemberWorks, r.FetchedWorks, total(r.TotalWorks.Value), when(time.Time(r.NextPollAt.Value)), dash(r.LastError.Value))
		}
		if len(sources.Items) < perPage || offset+perPage >= sources.Total {
			break
		}
	}
	tw.Flush()

	if ps := resp.PartialSeries; len(ps) > 0 {
		fmt.Fprintln(out, "\n== partial series (not a source; add one to mirror all of it)")
		tw = tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "SERIES\tHAVE\tMISSING PARTS\tMISSING WORK IDS")
		for _, p := range ps {
			fmt.Fprintf(tw, "%d\t%d/%d\t%s\t%#v\n", p.ID, p.Mirrored, p.WorksTotal, intList(p.MissingPositions, 10), p.MissingWorkIds)
		}
		tw.Flush()
	}

	fmt.Fprintln(out, "\n== recent request errors")
	reqs := resp.RequestErrors
	if len(reqs) == 0 {
		fmt.Fprintln(out, "none")
	}
	tw = tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, r := range reqs {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\tjob %d\n", time.Time(r.RequestedAt).Local().Format("01-02 15:04:05"), r.Status, r.Class, r.Path, r.Reason, r.JobID.Value)
	}
	tw.Flush()

	fmt.Fprintln(out, "\n== recent failed jobs")
	failed := resp.FailedJobs
	if len(failed) == 0 {
		fmt.Fprintln(out, "none")
	}
	tw = tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, j := range failed {
		fmt.Fprintf(tw, "job %d\t%s\t%s\tsource %d\t%s\t%s\t%s\n", j.ID, j.Kind, j.Target, j.SourceID.Value, when(time.Time(j.FinishedAt.Value)), j.LastErrorCode.Value, j.LastErrorDetail.Value)
	}
	tw.Flush()
	return 0
}

// runDebug prints the content-free trace of one job or one work.
func runDebug(ctx context.Context, a *app, args []string, out, errOut io.Writer) int {
	if len(args) != 2 || (args[0] != "job" && args[0] != "work") {
		fmt.Fprint(errOut, "usage: ao3mirror debug job ID | debug work ID\n")
		return 2
	}
	id, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		fmt.Fprintf(errOut, "bad id %q\n", args[1])
		return 2
	}
	switch args[0] {
	case "job":
		d, err := a.apiClient.DebugJob(ctx, apiclient.DebugJobParams{
			ID: apiclient.ID(id),
		})
		if err != nil {
			fmt.Fprintf(errOut, "debug job: %v\n", err)
			return 1
		}
		printJob(out, d.Job)
		fmt.Fprintln(out, "\nevents:")
		for _, e := range d.Events {
			// The fields that were set, as stored.
			fmt.Fprintf(out, "  %s  %-7s %#v\n", time.Time(e.OccurredAt).Local().Format("01-02 15:04:05"), e.Event, e)
		}
		printRequests(out, d.Requests)
	case "work":
		d, err := a.apiClient.DebugWork(ctx, apiclient.DebugWorkParams{
			ID: apiclient.ID(id),
		})
		if err != nil {
			fmt.Fprintf(errOut, "debug work: %v\n", err)
			return 1
		}
		if err := printWork(out, id, d.Work); err != nil {
			fmt.Fprintf(errOut, "debug work: %v\n", err)
			return 1
		}
		fmt.Fprintln(out, "\njobs:")
		for _, j := range d.Jobs {
			fmt.Fprintf(out, "  job %d %s class %d attempts %d/%d %s %s %s\n", j.ID, j.State, j.Priority, j.Attempts, j.MaxAttempts,
				when(time.Time(j.FinishedAt.Value)), dash(string(j.LastErrorCode.Value)), dash(j.LastErrorDetail.Value))
		}
		printRequests(out, d.Requests)
	}
	return 0
}

func printJob(out io.Writer, j apiclient.Job) {
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, kv := range [][2]string{
		{"id", strconv.FormatInt(int64(j.ID), 10)}, {"kind", string(j.Kind)}, {"target", j.Target}, {"source", strconv.FormatInt(int64(j.SourceID.Value), 10)},
		{"class", strconv.Itoa(j.Priority)}, {"state", string(j.State)}, {"attempts", fmt.Sprintf("%d/%d", j.Attempts, j.MaxAttempts)},
		{"not before", when(time.Time(j.NotBeforeAt.Value))}, {"created", when(time.Time(j.CreatedAt.Value))}, {"started", when(time.Time(j.StartedAt.Value))},
		{"finished", when(time.Time(j.FinishedAt.Value))}, {"last error code", dash(string(j.LastErrorCode.Value))}, {"last error detail", dash(j.LastErrorDetail.Value)},
	} {
		fmt.Fprintf(tw, "%s:\t%s\n", kv[0], kv[1])
	}
	tw.Flush()
}

func printRequests(out io.Writer, reqs []apiclient.Request) {
	fmt.Fprintln(out, "\nrequests:")
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, r := range reqs {
		fmt.Fprintf(tw, "  %s\t%s\t%d\t%s\t%s\t%dB\t%dms\twaited %s\t%s\n", time.Time(r.RequestedAt).Local().Format("01-02 15:04:05"), r.ReqID,
			r.Status, r.Class, r.Path, r.Bytes, time.Duration(r.Duration).Milliseconds(), time.Duration(r.Waited).Round(time.Second), r.Reason)
	}
	tw.Flush()
}

// printWork shows numeric/status fields only: no title, summary, names or tags.
func printWork(out io.Writer, id int64, d apiclient.WorkDiag) error {
	exp := "?"
	if d.ChaptersExpected.Set {
		exp = strconv.Itoa(d.ChaptersExpected.Value)
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, kv := range [][2]string{
		{"work", strconv.FormatInt(id, 10)}, {"status", string(d.Status)}, {"published", dash(d.PublishedOn.Value.Format("01-02 15:04:05"))},
		{"revised", dash(d.RevisedOn.Value.Format("01-02 15:04:05"))}, {"revised at last fetch", dash(d.FetchedRevisedOn.Value.Format("01-02 15:04:05"))}, {"words", comma(d.Words)},
		{"chapters", fmt.Sprintf("%d/%s", d.ChaptersPosted, exp)},
		{"kudos/hits/comments/bookmarks", fmt.Sprintf("%d/%d/%d/%d", d.Stats.Kudos, d.Stats.Hits, d.Stats.Comments, d.Stats.Bookmarks)},
		{"restricted/anonymous", fmt.Sprintf("%t/%t", d.Restricted, d.Anonymous)},
		{"tags/creators/chapter rows/sources", fmt.Sprintf("%d/%d/%d/%d", d.Rows.Tags, d.Rows.Creators, d.Rows.Chapters, d.Rows.Sources)},
		{"images", strconv.Itoa(d.Rows.Images)},
		{"raw versions", fmt.Sprintf("%d (%s)", d.Rows.RawPages, mb(int64(d.Rows.RawBytes)))}, {"chapter bodies", strconv.Itoa(d.Rows.ChapterBodies)},
		{"parser version", strconv.Itoa(d.ParserVersion)},
		{"first seen", when(time.Time(d.FirstSeenAt.Value))}, {"last listed", when(time.Time(d.LastListedAt.Value))},
		{"last fetched", when(time.Time(d.LastFetchedAt.Value))}, {"last checked", when(time.Time(d.LastCheckedAt.Value))},
		{"fetch failed", when(time.Time(d.FetchFailedAt.Value))},
	} {
		fmt.Fprintf(tw, "%s:\t%s\n", kv[0], kv[1])
	}
	return tw.Flush()
}

// localTime formats t in local time, or "" when zero.
func localTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// intList joins up to max ints with commas, then "+N more"; "-" when empty.
func intList(xs []int, max int) string {
	ids := make([]int64, len(xs))
	for i, x := range xs {
		ids[i] = int64(x)
	}
	return idList(ids, max)
}

func idList(xs []int64, max int) string {
	if len(xs) == 0 {
		return "-"
	}
	n := min(len(xs), max)
	parts := make([]string, n)
	for i := range n {
		parts[i] = strconv.FormatInt(xs[i], 10)
	}
	s := strings.Join(parts, ",")
	if len(xs) > n {
		s += fmt.Sprintf(" +%d more", len(xs)-n)
	}
	return s
}
