package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

const sourceUsage = `usage: ao3mirror source <command> [flags]

  add URL [--order kudos_desc] [--priority N] [--poll 6h] [--confirm]
        add an AO3 tag/user/pseud/bookmarks/series/collection/work URL. Fetches page 1 once
        (1 request) to estimate the backfill. Sources over sources.auto_confirm_max works wait
        for 'confirm' unless --confirm. --order: <field>_<asc|desc>, field one of
        author,bookmarks,comments,created,hits,kudos,revised,title,words.
  list [--names]           list sources
  show ID [--names]        one source in detail
  confirm ID               start a pending (large) source
  pause ID | resume ID     stop / restart scheduling
  cancel ID                stop for good and drop queued jobs (works are kept)
  remove ID                delete the source (works are kept)
  refresh ID [--full]      poll now and re-queue failed work fetches; --full re-walks the
                           whole listing (rescan) and clears a parse-failure block
  set ID [--poll 3h] [--priority N]   change poll interval / priority after adding

Tag/user/collection names are hidden (shown as hashes) unless --names is given.
`

func runSource(ctx context.Context, a *app, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errOut, sourceUsage)
		return 2
	}
	svc := a.ctl
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "add":
		fs := flag.NewFlagSet("source add", flag.ContinueOnError)
		fs.SetOutput(errOut)
		order := fs.String("order", "", "backfill order (default sources.default_backfill_order)")
		prio := fs.Int("priority", 0, "priority within a scheduling class (higher first)")
		poll := fs.Duration("poll", 0, "update poll interval (default sources.poll_interval)")
		confirm := fs.Bool("confirm", false, "skip the auto_confirm_max gate")
		names := fs.Bool("names", false, "show tag/user names")
		u, ok := parseWithPositional(fs, rest)
		if !ok {
			fmt.Fprint(errOut, sourceUsage)
			return 2
		}
		res, err := svc.AddSource(ctx, u, controllers.AddSourceOptions{Order: *order, Priority: *prio, PollInterval: *poll, Confirm: *confirm})
		if ex, ok := errors.AsType[controllers.ExistsError](err); ok {
			fmt.Fprintf(errOut, "already added as source %d (see: ao3mirror source show %d)\n", ex.ID, ex.ID)
			return 1
		}
		if err != nil {
			fmt.Fprintf(errOut, "add failed: %v\n", err)
			return 1
		}
		s, e := res.Source, res.Estimate
		fmt.Fprintf(out, "source %d added: kind=%s target=%s state=%s order=%s\n", s.ID, s.Kind, target(s, *names), s.State, s.BackfillOrder)
		fmt.Fprintf(out, "estimate: %s works, %s listing pages; ~%s requests (~%s at %s/request), ~%s on disk\n",
			comma(e.Total), comma(e.Pages), comma(e.Requests), human(e.Duration), a.cfg.Rate.Interval, mb(e.DiskBytes))
		if e.Windowed {
			fmt.Fprintf(out, "note: over 100,000 works; backfill will be sliced into date windows\n")
		}
		if res.Blurbs > 0 {
			fmt.Fprintf(out, "page 1: %d works stored\n", res.Blurbs)
		}
		if s.State == store.SourcePendingConfirm {
			fmt.Fprintf(out, "pending: more than sources.auto_confirm_max (%d) works. Start it with 'ao3mirror source confirm %d',\nor remove it and re-add a narrower URL (filters on the tag page).\n",
				a.cfg.Sources.AutoConfirmMax, s.ID)
		}
		return 0
	case "list":
		fs := flag.NewFlagSet("source list", flag.ContinueOnError)
		fs.SetOutput(errOut)
		names := fs.Bool("names", false, "show tag/user names")
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		rows, err := a.ctl.ListSources(ctx)
		if err != nil {
			fmt.Fprintf(errOut, "list: %v\n", err)
			return 1
		}
		tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tKIND\tTARGET\tFILTERS\tSTATE\tWORKS seen/fetched/total\tORDER\tNEXT POLL\tERROR")
		for _, r := range rows {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%d/%d/%s\t%s\t%s\t%s\n", r.ID, r.Kind, target(r, *names), filtersSummary(r, *names),
				r.State, r.MemberWorks, r.FetchedWorks, total(r.TotalWorks), r.BackfillOrder, when(r.NextPollAt), dash(r.LastError))
		}
		tw.Flush()
		return 0
	case "show":
		fs := flag.NewFlagSet("source show", flag.ContinueOnError)
		fs.SetOutput(errOut)
		names := fs.Bool("names", false, "show tag/user names")
		idStr, ok := parseWithPositional(fs, rest)
		if !ok {
			fmt.Fprint(errOut, sourceUsage)
			return 2
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			fmt.Fprintf(errOut, "bad id %q\n", idStr)
			return 2
		}
		r, err := a.ctl.GetSource(ctx, id)
		if err != nil {
			fmt.Fprintf(errOut, "show: %v\n", err)
			return 1
		}
		path := r.Path
		if !*names {
			u, _ := a.client.Resolve(r.Path)
			path = httpx.SafePath(u)
		}
		tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		for _, kv := range [][2]string{
			{"id", strconv.FormatInt(r.ID, 10)}, {"kind", r.Kind}, {"target", target(r, *names)},
			{"filters", filtersSummary(r, *names)}, {"path", path}, {"state", string(r.State)},
			{"order", r.BackfillOrder}, {"priority", strconv.Itoa(r.Priority)}, {"poll interval", r.PollInterval.String()},
			{"works seen", comma(r.MemberWorks)}, {"works fetched", comma(r.FetchedWorks)}, {"total (AO3)", total(r.TotalWorks)},
			{"est requests", total(r.EstRequests)}, {"backfill cursor", r.BackfillCursor},
			{"created", when(r.CreatedAt)}, {"last poll", when(r.LastPollAt)}, {"next poll", when(r.NextPollAt)},
			{"last full scan", when(r.LastFullScanAt)}, {"backfill done", when(r.BackfillDoneAt)}, {"last error", dash(r.LastError)},
		} {
			fmt.Fprintf(tw, "%s:\t%s\n", kv[0], kv[1])
		}
		tw.Flush()
		return 0
	case "refresh":
		fs := flag.NewFlagSet("source refresh", flag.ContinueOnError)
		fs.SetOutput(errOut)
		full := fs.Bool("full", false, "re-walk the whole listing now (rescan) and clear a parse-failure block")
		idStr, ok := parseWithPositional(fs, rest)
		if !ok {
			fmt.Fprint(errOut, sourceUsage)
			return 2
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			fmt.Fprintf(errOut, "bad id %q\n", idStr)
			return 2
		}
		res, err := svc.RefreshSource(ctx, id, *full)
		if err != nil {
			fmt.Fprintf(errOut, "refresh %d: %v\n", id, err)
			return 1
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
	case "set":
		fs := flag.NewFlagSet("source set", flag.ContinueOnError)
		fs.SetOutput(errOut)
		poll := fs.Duration("poll", 0, "update poll interval, e.g. 3h (>= 10m)")
		prio := fs.Int("priority", 0, "priority within a scheduling class (higher first)")
		idStr, ok := parseWithPositional(fs, rest)
		if !ok {
			fmt.Fprint(errOut, sourceUsage)
			return 2
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			fmt.Fprintf(errOut, "bad id %q\n", idStr)
			return 2
		}
		var prioPtr *int
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "priority" {
				prioPtr = prio
			}
		})
		if *poll == 0 && prioPtr == nil {
			fmt.Fprint(errOut, "set: give --poll and/or --priority\n")
			return 2
		}
		if err := svc.SetSourceSettings(ctx, id, *poll, prioPtr); err != nil {
			fmt.Fprintf(errOut, "set %d: %v\n", id, err)
			return 1
		}
		r, _ := a.ctl.GetSource(ctx, id)
		fmt.Fprintf(out, "source %d: poll every %s, priority %d, next poll %s\n", id, r.PollInterval, r.Priority, when(r.NextPollAt))
		return 0
	case "confirm", "pause", "resume", "cancel", "remove":
		if len(rest) != 1 {
			fmt.Fprint(errOut, sourceUsage)
			return 2
		}
		id, err := strconv.ParseInt(rest[0], 10, 64)
		if err != nil {
			fmt.Fprintf(errOut, "bad id %q\n", rest[0])
			return 2
		}
		var msg string
		switch cmd {
		case "confirm":
			err, msg = svc.ConfirmSource(ctx, id), "confirmed (active)"
		case "pause":
			err, msg = svc.PauseSource(ctx, id), "paused"
		case "resume":
			err, msg = svc.ResumeSource(ctx, id), "resumed (active)"
		case "cancel":
			var n int64
			n, err = svc.CancelSource(ctx, id)
			msg = fmt.Sprintf("cancelled (%d queued jobs dropped; mirrored works kept)", n)
		case "remove":
			err, msg = svc.RemoveSource(ctx, id), "removed (mirrored works kept)"
		}
		if err != nil {
			fmt.Fprintf(errOut, "%s %d: %v\n", cmd, id, err)
			return 1
		}
		fmt.Fprintf(out, "source %d %s\n", id, msg)
		return 0
	}
	fmt.Fprint(errOut, sourceUsage)
	return 2
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

func target(r store.SourceRow, names bool) string {
	if names || r.Kind == string(ao3url.KindWork) || r.Kind == string(ao3url.KindSeries) {
		return r.Target // ids are not names
	}
	return httpx.Hash(r.Target)
}

func filtersSummary(r store.SourceRow, names bool) string {
	if r.FiltersJSON == "{}" || r.FiltersJSON == "" {
		return "-"
	}
	if names {
		return r.FiltersJSON
	}
	return fmt.Sprintf("%d (hidden)", strings.Count(r.FiltersJSON, `":[`))
}

func total(n int) string {
	if n < 0 {
		return "?"
	}
	return comma(n)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func when(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

func comma(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return s
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func human(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%.1f days", d.Hours()/24)
	case d >= time.Hour:
		return fmt.Sprintf("%.1f h", d.Hours())
	default:
		return fmt.Sprintf("%d min", int(d.Minutes()+0.5))
	}
}

func mb(b int64) string {
	if b >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	}
	return fmt.Sprintf("%d MB", (b+(1<<20)-1)>>20)
}
