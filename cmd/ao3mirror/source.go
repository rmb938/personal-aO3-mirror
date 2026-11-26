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

const sourceUsage = `usage: ao3mirror source <command> [flags]

  add URL [--order kudos_desc] [--priority N] [--poll 6h] [--confirm] [--wait 2m] [--names]
        add an AO3 tag/user/pseud/bookmarks/series/collection/work URL. The daemon fetches
        page 1 once (1 request) to estimate the backfill; with a daemon running, add waits up
        to --wait for it, otherwise the add stays queued for 'ao3mirror run'. Sources over
        sources.auto_confirm_max works wait for 'confirm' unless --confirm. --order:
        <field>_<asc|desc>, field one of author,bookmarks,comments,created,hits,kudos,revised,
        title,words.
  list [--names]           list sources
  show ID [--names]        one source in detail
  confirm ID               start a pending (large) source
  pause ID | resume ID     stop / restart scheduling
  cancel ID                stop for good and drop queued jobs (works are kept)
  remove ID                delete the source (works are kept)
  refresh ID [--full]      poll now and re-queue failed work fetches; --full re-walks the
                           whole listing (rescan) and clears a parse-failure block
  set ID [--poll 3h] [--priority N]   change poll interval / priority after adding

Every source command talks to the API's admin socket, so 'ao3mirror serve' must be running.
Tag/user/collection names are hidden (shown as hashes) unless --names is given.
`

// listPerPage is the page size the CLI lists with: the API's maximum.
const listPerPage = 100

func runSource(ctx context.Context, a *app, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errOut, sourceUsage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "add":
		return sourceAdd(ctx, a, rest, out, errOut)
	case "list":
		return sourceList(ctx, a, rest, out, errOut)
	case "show":
		return sourceShow(ctx, a, rest, out, errOut)
	case "refresh":
		return sourceRefresh(ctx, a, rest, out, errOut)
	case "set":
		return sourceSet(ctx, a, rest, out, errOut)
	case "confirm", "pause", "resume", "cancel", "remove":
		return sourceAction(ctx, a, cmd, rest, out, errOut)
	}
	fmt.Fprint(errOut, sourceUsage)
	return 2
}

func sourceAdd(ctx context.Context, a *app, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("source add", flag.ContinueOnError)
	fs.SetOutput(errOut)
	order := fs.String("order", "", "backfill order (default sources.default_backfill_order)")
	prio := fs.Int("priority", 0, "priority within a scheduling class (higher first)")
	poll := fs.Duration("poll", 0, "update poll interval (default sources.poll_interval)")
	confirm := fs.Bool("confirm", false, "skip the auto_confirm_max gate")
	names := fs.Bool("names", false, "show tag/user names")
	wait := fs.Duration("wait", 2*time.Minute, "how long to wait for a running daemon to fetch page 1 (0: don't wait)")
	u, ok := parseWithPositional(fs, args)
	if !ok {
		fmt.Fprint(errOut, sourceUsage)
		return 2
	}
	req := &apiclient.SourceAddRequest{URL: u}
	if *order != "" {
		req.Order.SetTo(apiclient.Order(*order))
	}
	if flagSet(fs, "priority") {
		req.Priority.SetTo(*prio)
	}
	if *poll != 0 {
		req.PollInterval.SetTo(apiclient.Duration(*poll))
	}
	if *confirm {
		req.Confirm.SetTo(true)
	}
	res, err := a.client.AddSource(ctx, req, reveal(*names))
	if err != nil {
		return fail(errOut, "source add", err)
	}
	switch r := res.(type) {
	case *apiclient.SourceAddResultHeaders: // a single work, added at once
		printAdded(out, a, r.Response)
		return 0
	case *apiclient.SourceAddQueuedHeaders:
		return waitSourceAdd(ctx, a, r.Response, *wait, *names, out, errOut)
	case *apiclient.ProblemHeaders: // 409 already-exists
		if id, ok := idFromLocation(r.Location); ok {
			fmt.Fprintf(errOut, "source add: already added as source %d (see: ao3mirror source show %d)\n", id, id)
			return 1
		}
		fmt.Fprintf(errOut, "source add: %s\n", problemText(r.Response))
		return 1
	}
	fmt.Fprintf(errOut, "source add: unexpected response %T\n", res)
	return 1
}

// waitSourceAdd follows a queued add: with a daemon running it waits up to wait for page 1 and
// prints the result; otherwise the add stays queued for the next `ao3mirror run`.
func waitSourceAdd(ctx context.Context, a *app, q apiclient.SourceAddQueued, wait time.Duration, names bool, out, errOut io.Writer) int {
	verb := "queued"
	if !q.Created {
		verb = "already queued"
	}
	fmt.Fprintf(out, "source add %s as job %d (see: ao3mirror debug job %d)\n", verb, q.JobID, q.JobID)
	running, err := daemonRunning(a.data.root)
	if err != nil {
		fmt.Fprintf(errOut, "source add: checking for a running daemon: %v\n", err)
		return 1
	}
	if !running {
		fmt.Fprintln(out, "no daemon running: 'ao3mirror run' fetches page 1 (1 request) and adds the source")
		return 0
	}
	if wait <= 0 {
		return 0
	}
	fmt.Fprintln(out, "waiting for the daemon to fetch page 1...")
	deadline := time.Now().Add(wait)
	for {
		sa, err := a.client.GetSourceAdd(ctx, apiclient.GetSourceAddParams{JobID: q.JobID}, reveal(names))
		if err != nil {
			return fail(errOut, "source add", err)
		}
		switch sa.State {
		case apiclient.JobStateDone:
			if r, ok := sa.Result.Get(); ok {
				printAdded(out, a, r)
				return 0
			}
			fmt.Fprintf(errOut, "source add: job %d is done but has no result\n", q.JobID)
			return 1
		case apiclient.JobStateFailed, apiclient.JobStateCancelled:
			if id, ok := sa.ExistingSourceID.Get(); ok {
				fmt.Fprintf(errOut, "source add: already added as source %d (see: ao3mirror source show %d)\n", id, id)
				return 1
			}
			fmt.Fprintf(errOut, "source add: %s: %s\n", sa.State, jobError(sa.LastErrorCode, sa.LastErrorDetail))
			return 1
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(out, "still %s after %s (AO3 busy or a long queue); the daemon adds the source when it gets to it\n", sa.State, wait)
			return 0
		}
		select {
		case <-ctx.Done():
			fmt.Fprintln(out, "stopped waiting; the add stays queued")
			return 0
		case <-time.After(time.Second):
		}
	}
}

func printAdded(out io.Writer, a *app, res apiclient.SourceAddResult) {
	s, e := res.Source, res.Estimate
	fmt.Fprintf(out, "source %d added: kind=%s target=%s state=%s order=%s\n",
		s.ID, s.Kind, sourceTarget(s), s.State, dash(string(s.Order.Or(""))))
	fmt.Fprintf(out, "estimate: %s works, %s listing pages; ~%s requests (~%s at %s/request), ~%s on disk\n",
		comma(e.Total), comma(e.Pages), comma(e.Requests), human(time.Duration(e.Duration)), a.cfg.Rate.Interval, mb(e.DiskBytes))
	if e.Windowed {
		fmt.Fprintln(out, "note: over 100,000 works; backfill will be sliced into date windows")
	}
	if res.Blurbs > 0 {
		fmt.Fprintf(out, "page 1: %d works stored\n", res.Blurbs)
	}
	if s.State == apiclient.SourceStatePendingConfirm {
		fmt.Fprintf(out, "pending: more than sources.auto_confirm_max (%d) works. Start it with 'ao3mirror source confirm %d',\nor remove it and re-add a narrower URL (filters on the tag page).\n",
			a.cfg.Sources.AutoConfirmMax, s.ID)
	}
}

func sourceList(ctx context.Context, a *app, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("source list", flag.ContinueOnError)
	fs.SetOutput(errOut)
	names := fs.Bool("names", false, "show tag/user names")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprint(errOut, sourceUsage)
		return 2
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tTARGET\tFILTERS\tSTATE\tWORKS seen/fetched/total\tORDER\tNEXT POLL\tERROR")
	err := eachSource(ctx, a, *names, func(s apiclient.Source) {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%d/%d/%s\t%s\t%s\t%s\n", s.ID, s.Kind, sourceTarget(s), filters(s.Filters, *names),
			s.State, s.MemberWorks, s.FetchedWorks, nilTotal(s.TotalWorks), dash(string(s.Order.Or(""))), when(s.NextPollAt), dash(s.LastError.Or("")))
	})
	tw.Flush()
	if err != nil {
		return fail(errOut, "source list", err)
	}
	return 0
}

// eachSource calls fn for every source, paging through the list. Each page is its own snapshot,
// so a source added or removed meanwhile can shift a row across a page boundary.
func eachSource(ctx context.Context, a *app, names bool, fn func(apiclient.Source)) error {
	for page := 1; ; page++ {
		l, err := a.client.ListSources(ctx, apiclient.ListSourcesParams{
			Page: apiclient.NewOptInt(page), PerPage: apiclient.NewOptInt(listPerPage),
		}, reveal(names))
		if err != nil {
			return err
		}
		for _, s := range l.Items {
			fn(s)
		}
		if len(l.Items) < listPerPage || page*listPerPage >= l.Total {
			return nil
		}
	}
}

func sourceShow(ctx context.Context, a *app, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("source show", flag.ContinueOnError)
	fs.SetOutput(errOut)
	names := fs.Bool("names", false, "show tag/user names")
	arg, ok := parseWithPositional(fs, args)
	if !ok {
		fmt.Fprint(errOut, sourceUsage)
		return 2
	}
	id, ok := parseID(errOut, arg)
	if !ok {
		return 2
	}
	s, err := a.client.GetSource(ctx, apiclient.GetSourceParams{ID: id}, reveal(*names))
	if err != nil {
		return fail(errOut, "source show", err)
	}
	printFields(out, [][2]string{
		{"id", strconv.Itoa(int(s.ID))}, {"kind", string(s.Kind)}, {"target", sourceTarget(*s)},
		{"filters", filters(s.Filters, *names)}, {"path", s.Path}, {"state", string(s.State)},
		{"order", dash(string(s.Order.Or("")))}, {"priority", strconv.Itoa(s.Priority)},
		{"poll interval", time.Duration(s.PollInterval).String()},
		{"works seen", comma(s.MemberWorks)}, {"works fetched", comma(s.FetchedWorks)}, {"total (AO3)", nilTotal(s.TotalWorks)},
		{"est requests", nilTotal(s.EstRequests)}, {"backfill cursor", backfill(s.Backfill)},
		{"created", when(s.CreatedAt)}, {"last poll", when(s.LastPollAt)}, {"next poll", when(s.NextPollAt)},
		{"last full scan", when(s.LastFullScanAt)}, {"backfill done", when(s.BackfillDoneAt)},
		{"last error", dash(s.LastError.Or(""))},
	})
	return 0
}

func sourceRefresh(ctx context.Context, a *app, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("source refresh", flag.ContinueOnError)
	fs.SetOutput(errOut)
	full := fs.Bool("full", false, "re-walk the whole listing now (rescan) and clear a parse-failure block")
	arg, ok := parseWithPositional(fs, args)
	if !ok {
		fmt.Fprint(errOut, sourceUsage)
		return 2
	}
	id, ok := parseID(errOut, arg)
	if !ok {
		return 2
	}
	req := &apiclient.SourceRefreshRequest{}
	if *full {
		req.Full.SetTo(true)
	}
	res, err := a.client.RefreshSource(ctx, req, apiclient.RefreshSourceParams{ID: id})
	if err != nil {
		return fail(errOut, "source refresh", err)
	}
	fmt.Fprintf(out, "source %d: poll due now, %d failed work fetches re-queued", id, res.RequeuedWorks)
	if res.RescanStarted {
		fmt.Fprint(out, ", full re-walk started")
	}
	if res.UnblockedParser {
		fmt.Fprint(out, ", parse-failure block cleared")
	}
	fmt.Fprintln(out, " (the running daemon picks this up within seconds)")
	return 0
}

func sourceSet(ctx context.Context, a *app, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("source set", flag.ContinueOnError)
	fs.SetOutput(errOut)
	poll := fs.Duration("poll", 0, "update poll interval, e.g. 3h (>= 10m)")
	prio := fs.Int("priority", 0, "priority within a scheduling class (higher first)")
	arg, ok := parseWithPositional(fs, args)
	if !ok {
		fmt.Fprint(errOut, sourceUsage)
		return 2
	}
	id, ok := parseID(errOut, arg)
	if !ok {
		return 2
	}
	req := &apiclient.SourceSettingsRequest{}
	if *poll != 0 {
		req.PollInterval.SetTo(apiclient.Duration(*poll))
	}
	if flagSet(fs, "priority") {
		req.Priority.SetTo(*prio)
	}
	if !req.PollInterval.Set && !req.Priority.Set {
		fmt.Fprint(errOut, "source set: give --poll and/or --priority\n")
		return 2
	}
	if err := a.client.SetSourceSettings(ctx, req, apiclient.SetSourceSettingsParams{ID: id}); err != nil {
		return fail(errOut, "source set", err)
	}
	s, err := a.client.GetSource(ctx, apiclient.GetSourceParams{ID: id})
	if err != nil {
		return fail(errOut, "source set", err)
	}
	fmt.Fprintf(out, "source %d: poll every %s, priority %d, next poll %s\n",
		id, time.Duration(s.PollInterval), s.Priority, when(s.NextPollAt))
	return 0
}

// sourceAction runs one of the state changes that take only an id.
func sourceAction(ctx context.Context, a *app, cmd string, args []string, out, errOut io.Writer) int {
	if len(args) != 1 {
		fmt.Fprint(errOut, sourceUsage)
		return 2
	}
	id, ok := parseID(errOut, args[0])
	if !ok {
		return 2
	}
	var err error
	var msg string
	switch cmd {
	case "confirm":
		err, msg = a.client.ConfirmSource(ctx, apiclient.ConfirmSourceParams{ID: id}), "confirmed (active)"
	case "pause":
		err, msg = a.client.PauseSource(ctx, apiclient.PauseSourceParams{ID: id}), "paused"
	case "resume":
		err, msg = a.client.ResumeSource(ctx, apiclient.ResumeSourceParams{ID: id}), "resumed (active)"
	case "cancel":
		var res *apiclient.SourceCancelled
		if res, err = a.client.CancelSource(ctx, apiclient.CancelSourceParams{ID: id}); err == nil {
			msg = fmt.Sprintf("cancelled (%d queued jobs dropped; mirrored works kept)", res.DroppedJobs)
		}
	case "remove":
		err, msg = a.client.RemoveSource(ctx, apiclient.RemoveSourceParams{ID: id}), "removed (mirrored works kept)"
	}
	if err != nil {
		return fail(errOut, fmt.Sprintf("source %s %d", cmd, id), err)
	}
	fmt.Fprintf(out, "source %d %s\n", id, msg)
	return 0
}

// sourceTarget is a series or work source's numeric id, otherwise its target (a hash unless
// revealed).
func sourceTarget(s apiclient.Source) string {
	if id, ok := s.TargetID.Get(); ok {
		return strconv.Itoa(int(id))
	}
	return s.Target
}

// filters lists a source's work_search filters as "name=v1,v2 ..." with --names, otherwise
// only how many there are; "-" when none.
func filters(fs []apiclient.SourceFiltersItem, names bool) string {
	if len(fs) == 0 {
		return "-"
	}
	if !names {
		return fmt.Sprintf("%d (hidden)", len(fs))
	}
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = f.Name + "=" + strings.Join(f.Values, ",")
	}
	return strings.Join(parts, " ")
}

// backfill describes a backfill cursor, or "-" when the source has none.
func backfill(o apiclient.OptBackfill) string {
	b, ok := o.Get()
	if !ok {
		return "-"
	}
	s := "listing walk done"
	if b.NextPage > 0 {
		s = "next page " + strconv.Itoa(b.NextPage)
	}
	if ord, ok := b.Order.Get(); ok {
		s += ", order " + string(ord)
	}
	if b.WindowStartOn.Set || b.WindowEndOn.Set {
		s += ", window " + date(b.WindowStartOn) + ".." + date(b.WindowEndOn)
	}
	if b.Rescan.Or(false) {
		s += ", rescan since " + when(b.RescanStartedAt)
	}
	if p, ok := b.BlockedParser.Get(); ok {
		s += fmt.Sprintf(", blocked on parser %d", p)
	}
	if b.RetryAt.Set {
		s += ", retry at " + when(b.RetryAt)
	}
	return s
}

// printFields prints "name:\tvalue" rows aligned.
func printFields(out io.Writer, rows [][2]string) {
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, kv := range rows {
		fmt.Fprintf(tw, "%s:\t%s\n", kv[0], kv[1])
	}
	tw.Flush()
}

// flagSet reports whether the named flag was given on the command line.
func flagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

// parseWithPositional allows flags before or after one positional argument.
func parseWithPositional(fs *flag.FlagSet, args []string) (string, bool) {
	if err := fs.Parse(args); err != nil || fs.NArg() < 1 {
		return "", false
	}
	pos := fs.Arg(0)
	if rest := fs.Args()[1:]; len(rest) > 0 {
		if err := fs.Parse(rest); err != nil || fs.NArg() != 0 {
			return "", false
		}
	}
	return pos, true
}
