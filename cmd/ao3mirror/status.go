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

	"github.com/rmb938/personal-aO3-mirror/internal/jobs"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// runStatus prints a content-free overview: daemon, pacer, works, queue, requests, sources,
// recent errors. Safe for Claude to read (see CLAUDE.md).
func runStatus(ctx context.Context, a *app, out, errOut io.Writer) int {
	c, err := a.store.Counts(ctx)
	if err != nil {
		fmt.Fprintf(errOut, "status: %v\n", err)
		return 1
	}
	ds, at, _ := a.store.DaemonState(ctx)
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
	fmt.Fprintf(out, "raw pages: %s (%s raw, %s stored)\n", comma(c.RawPages), mb(c.RawBytes), mb(c.RawZstdBytes))
	if ic, ib, err := a.store.ImageCounts(ctx); err == nil && len(ic) > 0 {
		fmt.Fprintf(out, "images: %s (%s stored)\n", kv(ic), mb(ib))
	}

	fmt.Fprintln(out, "\n== queue")
	jc, _ := a.store.JobCounts(ctx)
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
	rows, _ := a.store.ListSources(ctx)
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tTARGET\tSTATE\tseen/fetched/total\tNEXT POLL\tERROR")
	for _, r := range rows {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d/%d/%s\t%s\t%s\n", r.ID, r.Kind, target(r, false), r.State,
			r.MemberWorks, r.FetchedWorks, total(r.TotalWorks), when(r.NextPollAt), dash(r.LastError))
	}
	tw.Flush()

	fmt.Fprintln(out, "\n== recent request errors")
	reqs, _ := a.store.RecentRequests(ctx, true, 0, "", 10)
	if len(reqs) == 0 {
		fmt.Fprintln(out, "none")
	}
	tw = tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, r := range reqs {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\tjob %d\n", r.Time.Local().Format("01-02 15:04:05"), r.Status, r.Class, r.Path, r.Reason, r.JobID)
	}
	tw.Flush()

	fmt.Fprintln(out, "\n== recent failed jobs")
	failed, _ := a.store.ListJobs(ctx, store.JobFailed, 0, "", 10)
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
		j, err := a.store.GetJob(ctx, id)
		if err != nil {
			fmt.Fprintf(errOut, "debug job: %v\n", err)
			return 1
		}
		printJob(out, j)
		evs, _ := a.store.JobEvents(ctx, id)
		fmt.Fprintln(out, "\nevents:")
		for _, e := range evs {
			fmt.Fprintf(out, "  %s  %-7s %s\n", e.Time.Local().Format("01-02 15:04:05"), e.Event, e.Detail)
		}
		printRequests(ctx, a, out, id, "")
	case "work":
		if err := printWork(ctx, a, out, id); err != nil {
			fmt.Fprintf(errOut, "debug work: %v\n", err)
			return 1
		}
		js, _ := a.store.ListJobs(ctx, "", 0, jobs.WorkKey(id), 20)
		fmt.Fprintln(out, "\njobs:")
		for _, j := range js {
			fmt.Fprintf(out, "  job %d %s class %d attempts %d/%d %s %s\n", j.ID, j.State, j.Priority, j.Attempts, j.MaxAttempts,
				when(j.FinishedAt), dash(j.LastError))
		}
		printRequests(ctx, a, out, 0, "/works/"+strconv.FormatInt(id, 10))
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

func printRequests(ctx context.Context, a *app, out io.Writer, jobID int64, prefix string) {
	reqs, _ := a.store.RecentRequests(ctx, false, jobID, prefix, 20)
	fmt.Fprintln(out, "\nrequests:")
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, r := range reqs {
		fmt.Fprintf(tw, "  %s\t%s\t%d\t%s\t%s\t%dB\t%dms\twaited %s\t%s\n", r.Time.Local().Format("01-02 15:04:05"), r.ReqID,
			r.Status, r.Class, r.Path, r.Bytes, r.Duration.Milliseconds(), r.Waited.Round(time.Second), r.Reason)
	}
	tw.Flush()
}

// printWork shows numeric/status fields only: no title, summary, names or tags.
func printWork(ctx context.Context, a *app, out io.Writer, id int64) error {
	var status, published, revised, fetchedRevised string
	var words, chPosted, chExpected, kudos, hits, comments, bookmarks, restricted, anonymous, parserVersion int
	var firstSeen, lastListed, lastFetched, lastChecked int64
	err := a.store.Meta.QueryRowContext(ctx, `SELECT status, published, revised_at, fetched_revised, words, chapters_posted,
		chapters_expected, kudos, hits, comments, bookmarks, restricted, anonymous, parser_version, first_seen, last_listed,
		last_fetched, last_checked FROM works WHERE id = ?`, id).Scan(&status, &published, &revised, &fetchedRevised, &words,
		&chPosted, &chExpected, &kudos, &hits, &comments, &bookmarks, &restricted, &anonymous, &parserVersion,
		&firstSeen, &lastListed, &lastFetched, &lastChecked)
	if err != nil {
		return err
	}
	var tags, creators, chapters, sources int
	a.store.Meta.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_tags WHERE work_id = ?`, id).Scan(&tags)
	a.store.Meta.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_creators WHERE work_id = ?`, id).Scan(&creators)
	a.store.Meta.QueryRowContext(ctx, `SELECT COUNT(*) FROM chapters WHERE work_id = ?`, id).Scan(&chapters)
	a.store.Meta.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_works WHERE work_id = ?`, id).Scan(&sources)
	var rawVersions, rawBytes, bodies int
	a.store.Content.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size),0) FROM raw_pages WHERE work_id = ?`, id).Scan(&rawVersions, &rawBytes)
	a.store.Content.QueryRowContext(ctx, `SELECT COUNT(*) FROM chapter_bodies WHERE work_id = ?`, id).Scan(&bodies)
	exp := "?"
	if chExpected > 0 {
		exp = strconv.Itoa(chExpected)
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, kv := range [][2]string{
		{"work", strconv.FormatInt(id, 10)}, {"status", status}, {"published", dash(published)}, {"revised", dash(revised)},
		{"revised at last fetch", dash(fetchedRevised)}, {"words", comma(words)}, {"chapters", fmt.Sprintf("%d/%s", chPosted, exp)},
		{"kudos/hits/comments/bookmarks", fmt.Sprintf("%d/%d/%d/%d", kudos, hits, comments, bookmarks)},
		{"restricted/anonymous", fmt.Sprintf("%t/%t", restricted == 1, anonymous == 1)},
		{"tags/creators/chapter rows/sources", fmt.Sprintf("%d/%d/%d/%d", tags, creators, chapters, sources)},
		{"raw versions", fmt.Sprintf("%d (%s)", rawVersions, mb(int64(rawBytes)))}, {"chapter bodies", strconv.Itoa(bodies)},
		{"parser version", strconv.Itoa(parserVersion)},
		{"first seen", when(unix(firstSeen))}, {"last listed", when(unix(lastListed))},
		{"last fetched", when(unix(lastFetched))}, {"last checked", when(unix(lastChecked))},
	} {
		fmt.Fprintf(tw, "%s:\t%s\n", kv[0], kv[1])
	}
	return tw.Flush()
}

func unix(s int64) time.Time {
	if s == 0 {
		return time.Time{}
	}
	return time.Unix(s, 0)
}

func localRFC(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04:05")
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
