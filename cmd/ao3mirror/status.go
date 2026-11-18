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

	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// runStatus prints a content-free overview: daemon, pacer, works, queue, requests, sources,
// recent errors. Safe for Claude to read (see CLAUDE.md).
func runStatus(ctx context.Context, a *app, out, errOut io.Writer) int {
	rep, err := a.ctl.Status(ctx)
	if err != nil {
		fmt.Fprintf(errOut, "status: %v\n", err)
		return 1
	}
	c, ds, at := rep.Counts, rep.Daemon, rep.DaemonAt
	fmt.Fprintln(out, "== daemon")
	switch {
	case at.IsZero():
		fmt.Fprintln(out, "never ran")
	default:
		age := time.Since(at).Round(time.Second)
		state := "running"
		if age > 2*time.Minute || ds["phase"] == "stopped" {
			state = "not running"
		}
		fmt.Fprintf(out, "%s (pid %s, phase %s, current job %s, heartbeat %s ago)\n", state, ds["pid"], ds["phase"], ds["current_job"], age)
		if w := localRFC(ds["waiting_until"]); w != "" && state == "running" {
			fmt.Fprintf(out, "waiting for next request slot until %s (pacing/backoff)\n", w)
		}
		fmt.Fprintf(out, "pacer: interval %s, slow factor %s, consecutive failures %s, breaker open %s, backoff until %s\n",
			ds["interval"], ds["slow_factor"], ds["consec_fails"], ds["breaker_open"], dash(localRFC(ds["backoff_until"])))
		if ds["daily_budget"] != "0" && ds["daily_budget"] != "" {
			fmt.Fprintf(out, "daily budget: %s / %s requests today\n", ds["day_count"], ds["daily_budget"])
		}
	}

	fmt.Fprintln(out, "\n== works")
	fmt.Fprintf(out, "by status: %s\n", kv(c.WorksByStatus))
	fmt.Fprintf(out, "fetched: %s, awaiting first fetch: %s, tags: %s, creators: %s, series: %s\n",
		comma(c.WorksFetched), comma(c.WorksNeverFetch), comma(c.Tags), comma(c.Creators), comma(c.Series))
	if c.ParseFailures > 0 {
		fmt.Fprintf(out, "parse failures: %s (raw pages kept and debug skeletons saved; run reparse after a parser fix)\n", comma(c.ParseFailures))
	}
	fmt.Fprintf(out, "raw pages: %s (%s raw, %s stored)\n", comma(c.RawPages), mb(c.RawBytes), mb(c.RawZstdBytes))
	if len(rep.Images) > 0 {
		fmt.Fprintf(out, "images: %s (%s stored)\n", kv(rep.Images), mb(rep.ImageBytes))
	}
	if ti := rep.TagInfo; ti.Total > 0 {
		fmt.Fprintf(out, "tag info: %s/%s fetched, %s due (%s used by mirrored works)\n",
			comma(ti.Fetched), comma(ti.Total), comma(ti.Due), strings.Join(ti.Types, "/"))
	}

	fmt.Fprintln(out, "\n== queue")
	jc := rep.Jobs
	kinds := make([]string, 0, len(jc))
	for k := range jc {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	if len(kinds) == 0 {
		fmt.Fprintln(out, "empty")
	}
	for _, k := range kinds {
		fmt.Fprintf(out, "%-9s %s\n", k, kv(jc[k]))
	}

	fmt.Fprintln(out, "\n== requests")
	fmt.Fprintf(out, "last 1h: %d, last 24h: %d, 24h by class: %s\n", c.Requests1h, c.Requests24h, kv(c.RequestsByClass))

	fmt.Fprintln(out, "\n== sources")
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tTARGET\tSTATE\tseen/fetched/total\tNEXT POLL\tERROR")
	for _, r := range rep.Sources {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d/%d/%s\t%s\t%s\n", r.ID, r.Kind, target(r, false), r.State,
			r.MemberWorks, r.FetchedWorks, total(r.TotalWorks), when(r.NextPollAt), dash(r.LastError))
	}
	tw.Flush()

	if ps := rep.PartialSeries; len(ps) > 0 {
		fmt.Fprintln(out, "\n== partial series (not a source; add one to mirror all of it)")
		tw = tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "SERIES\tHAVE\tMISSING PARTS\tMISSING WORK IDS")
		for _, p := range ps {
			fmt.Fprintf(tw, "%d\t%d/%d\t%s\t%s\n", p.ID, p.Mirrored, p.WorksTotal, intList(p.MissingPositions, 10), idList(p.MissingWorkIDs, 5))
		}
		tw.Flush()
	}

	fmt.Fprintln(out, "\n== recent request errors")
	reqs := rep.RequestErrors
	if len(reqs) == 0 {
		fmt.Fprintln(out, "none")
	}
	tw = tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, r := range reqs {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\tjob %d\n", r.Time.Local().Format("01-02 15:04:05"), r.Status, r.Class, r.Path, r.Reason, r.JobID)
	}
	tw.Flush()

	fmt.Fprintln(out, "\n== recent failed jobs")
	failed := rep.FailedJobs
	if len(failed) == 0 {
		fmt.Fprintln(out, "none")
	}
	tw = tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, j := range failed {
		fmt.Fprintf(tw, "job %d\t%s\t%s\tsource %d\t%s\t%s\n", j.ID, j.Kind, j.Target, j.SourceID, when(j.FinishedAt), j.LastError)
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
		d, err := a.ctl.DebugJob(ctx, id)
		if err != nil {
			fmt.Fprintf(errOut, "debug job: %v\n", err)
			return 1
		}
		printJob(out, d.Job)
		fmt.Fprintln(out, "\nevents:")
		for _, e := range d.Events {
			fmt.Fprintf(out, "  %s  %-7s %s\n", e.Time.Local().Format("01-02 15:04:05"), e.Event, e.Detail)
		}
		printRequests(out, d.Requests)
	case "work":
		d, err := a.ctl.DebugWork(ctx, id)
		if err != nil {
			fmt.Fprintf(errOut, "debug work: %v\n", err)
			return 1
		}
		if err := printWork(out, id, d.Diag); err != nil {
			fmt.Fprintf(errOut, "debug work: %v\n", err)
			return 1
		}
		fmt.Fprintln(out, "\njobs:")
		for _, j := range d.Jobs {
			fmt.Fprintf(out, "  job %d %s class %d attempts %d/%d %s %s\n", j.ID, j.State, j.Priority, j.Attempts, j.MaxAttempts,
				when(j.FinishedAt), dash(j.LastError))
		}
		printRequests(out, d.Requests)
	}
	return 0
}

func printJob(out io.Writer, j store.Job) {
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, kv := range [][2]string{
		{"id", strconv.FormatInt(j.ID, 10)}, {"kind", j.Kind}, {"target", j.Target}, {"source", strconv.FormatInt(j.SourceID, 10)},
		{"class", strconv.Itoa(j.Priority)}, {"state", j.State}, {"attempts", fmt.Sprintf("%d/%d", j.Attempts, j.MaxAttempts)},
		{"not before", when(j.NotBefore)}, {"created", when(j.CreatedAt)}, {"started", when(j.StartedAt)},
		{"finished", when(j.FinishedAt)}, {"last error", dash(j.LastError)}, {"dedupe", j.DedupeKey},
	} {
		fmt.Fprintf(tw, "%s:\t%s\n", kv[0], kv[1])
	}
	tw.Flush()
}

func printRequests(out io.Writer, reqs []store.RequestRow) {
	fmt.Fprintln(out, "\nrequests:")
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, r := range reqs {
		fmt.Fprintf(tw, "  %s\t%s\t%d\t%s\t%s\t%dB\t%dms\twaited %s\t%s\n", r.Time.Local().Format("01-02 15:04:05"), r.ReqID,
			r.Status, r.Class, r.Path, r.Bytes, r.Duration.Milliseconds(), r.Waited.Round(time.Second), r.Reason)
	}
	tw.Flush()
}

// printWork shows numeric/status fields only: no title, summary, names or tags.
func printWork(out io.Writer, id int64, d store.WorkDiag) error {
	exp := "?"
	if d.ChaptersExpected > 0 {
		exp = strconv.Itoa(d.ChaptersExpected)
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, kv := range [][2]string{
		{"work", strconv.FormatInt(id, 10)}, {"status", string(d.Status)}, {"published", dash(d.Published)},
		{"revised", dash(d.Revised)}, {"revised at last fetch", dash(d.FetchedRevised)}, {"words", comma(d.Words)},
		{"chapters", fmt.Sprintf("%d/%s", d.ChaptersPosted, exp)},
		{"kudos/hits/comments/bookmarks", fmt.Sprintf("%d/%d/%d/%d", d.Stats.Kudos, d.Stats.Hits, d.Stats.Comments, d.Stats.Bookmarks)},
		{"restricted/anonymous", fmt.Sprintf("%t/%t", d.Restricted, d.Anonymous)},
		{"tags/creators/chapter rows/sources", fmt.Sprintf("%d/%d/%d/%d", d.Tags, d.Creators, d.ChapterRows, d.Sources)},
		{"images", strconv.Itoa(d.Images)},
		{"raw versions", fmt.Sprintf("%d (%s)", d.RawVersions, mb(d.RawBytes))}, {"chapter bodies", strconv.Itoa(d.ChapterBodies)},
		{"parser version", strconv.Itoa(d.ParserVersion)},
		{"first seen", when(d.FirstSeen)}, {"last listed", when(d.LastListed)},
		{"last fetched", when(d.LastFetched)}, {"last checked", when(d.LastChecked)},
		{"fetch failed", when(d.FetchFailedAt)},
	} {
		fmt.Fprintf(tw, "%s:\t%s\n", kv[0], kv[1])
	}
	return tw.Flush()
}

func localRFC(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
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

func kv(m map[string]int) string {
	if len(m) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + comma(m[k])
	}
	return strings.Join(parts, " ")
}
