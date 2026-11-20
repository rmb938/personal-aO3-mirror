package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/apiclient"
	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

const sourceUsage = `usage: ao3mirror source <command> [flags]

  add URL [--order kudos_desc] [--priority N] [--poll 6h] [--confirm] [--wait 2m]
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

Tag/user/collection names are hidden (shown as hashes) unless --names is given.
`

// waitSourceAdd follows a queued add: with a daemon running it waits up to wait for page 1 and
// prints the result; otherwise the add stays queued for the next `ao3mirror run`.
func waitSourceAdd(ctx context.Context, apiClient *apiclient.Client, cfg config.Config, q apiclient.SourceAddQueuedHeaders, wait time.Duration, out, errOut io.Writer) int {
	verb := "queued"
	if !q.Response.Created {
		verb = "already queued"
	}
	fmt.Fprintf(out, "source add %s as job %d (see: ao3mirror debug job %d)\n", verb, q.Response.JobID, q.Response.JobID)
	running, err := daemonRunning(cfg.DataDir)
	if err != nil {
		fmt.Fprintf(errOut, "add: checking for a running daemon: %v\n", err)
		return 1
	}
	if !running {
		fmt.Fprintf(out, "no daemon running: 'ao3mirror run' fetches page 1 (1 request) and adds the source\n")
		return 0
	}
	if wait <= 0 {
		return 0
	}
	fmt.Fprintf(out, "waiting for the daemon to fetch page 1...\n")
	deadline := time.Now().Add(wait)
	for {
		sourceAdd, err := apiClient.GetSourceAdd(ctx, apiclient.GetSourceAddParams{
			JobID: q.Response.JobID,
		})
		if err != nil {
			fmt.Fprintf(errOut, "add: %v\n", err)
			return 1
		}
		switch sourceAdd.State {
		case model.JobDone:
			printAdded(out, cfg, apiclient.SourceAddResultHeaders{
				Response: sourceAdd.Result.Value,
			})
			return 0
		case apiclient.JobStateFailed, apiclient.JobStateCancelled:
			if id := sourceAdd.ExistingSourceID.Value; id != 0 {
				fmt.Fprintf(errOut, "already added as source %d (see: ao3mirror source show %d)\n", id, id)
				return 1
			}
			fmt.Fprintf(errOut, "add failed: %s: %s\n", dash(string(sourceAdd.LastErrorCode.Value)), dash(sourceAdd.LastErrorDetail.Value))
			return 1
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(out, "still %s after %s (AO3 busy or a long queue); the daemon adds the source when it gets to it\n", sourceAdd.State, wait)
			return 0
		}
		select {
		case <-ctx.Done():
			fmt.Fprintf(out, "stopped waiting; the add stays queued\n")
			return 0
		case <-time.After(time.Second):
		}
	}
}

func printAdded(out io.Writer, cfg config.Config, res apiclient.SourceAddResultHeaders) {
	s, e := res.Response.Source, res.Response.Estimate
	fmt.Fprintf(out, "source %d added: kind=%s target=%s state=%s order=%s\n", s.ID, s.Kind, s.Target, s.State, s.Order.Value)
	fmt.Fprintf(out, "estimate: %s works, %s listing pages; ~%s requests (~%s at %s/request), ~%s on disk\n",
		comma(e.Total), comma(e.Pages), comma(e.Requests), human(time.Duration(e.Duration)), cfg.Rate.Interval, mb(int64(e.DiskBytes)))
	if e.Windowed {
		fmt.Fprintf(out, "note: over 100,000 works; backfill will be sliced into date windows\n")
	}
	if res.Response.Blurbs > 0 {
		fmt.Fprintf(out, "page 1: %d works stored\n", res.Response.Blurbs)
	}
	if s.State == apiclient.SourceStatePendingConfirm {
		fmt.Fprintf(out, "pending: more than sources.auto_confirm_max (%d) works. Start it with 'ao3mirror source confirm %d',\nor remove it and re-add a narrower URL (filters on the tag page).\n",
			cfg.Sources.AutoConfirmMax, s.ID)
	}
}

func runSource(ctx context.Context, a *app, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errOut, sourceUsage)
		return 2
	}
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
		wait := fs.Duration("wait", 2*time.Minute, "how long to wait for a running daemon to fetch page 1 (0: don't wait)")
		u, ok := parseWithPositional(fs, rest)
		if !ok {
			fmt.Fprint(errOut, sourceUsage)
			return 2
		}

		sourceAddRequest := &apiclient.SourceAddRequest{
			URL: u,
		}

		if len(*order) > 0 {
			sourceAddRequest.Order = apiclient.NewOptOrder(apiclient.Order(*order))
		}
		if *prio != 0 {
			sourceAddRequest.Priority = apiclient.NewOptInt(*prio)
		}
		if *poll != 0 {
			sourceAddRequest.PollInterval = apiclient.NewOptDuration(apiclient.Duration(*poll))
		}
		if *confirm {
			sourceAddRequest.Confirm = apiclient.NewOptBool(true)
		}

		resp, err := a.apiClient.AddSource(ctx, sourceAddRequest, apiclient.WithEditRequest(func(req *http.Request) error {
			q := req.URL.Query()

			if *names {
				q.Add(runtime.RevealParam, "1")
			}

			req.URL.RawQuery = q.Encode()
			return nil
		}))
		if err != nil {
			fmt.Fprintf(errOut, "add failed: %v\n", err)
			return 1
		}

		if queued, ok := resp.(*apiclient.SourceAddQueuedHeaders); ok {
			return waitSourceAdd(ctx, a.apiClient, a.cfg, *queued, *wait, out, errOut)
		} else if result, ok := resp.(*apiclient.SourceAddResultHeaders); ok {
			printAdded(out, a.cfg, *result)
			return 0
		}

		fmt.Fprintf(errOut, "Unknown Add Source Result Type: %v\n", resp)
		return 1
	case "list":
		fs := flag.NewFlagSet("source list", flag.ContinueOnError)
		fs.SetOutput(errOut)
		names := fs.Bool("names", false, "show tag/user names")
		if err := fs.Parse(rest); err != nil {
			return 2
		}

		tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tKIND\tTARGET\tFILTERS\tSTATE\tWORKS seen/fetched/total\tORDER\tNEXT POLL\tERROR")
		// Page through like the API client will (stage 6). Each page is its own snapshot, so a
		// source added or removed meanwhile can shift a row across a page boundary.
		const perPage = 100
		for offset := 0; ; offset += perPage {
			sourceList, err := a.apiClient.ListSources(ctx, apiclient.ListSourcesParams{
				Page:    apiclient.NewOptInt(offset + 1),
				PerPage: apiclient.NewOptInt(perPage),
			}, apiclient.WithEditRequest(func(req *http.Request) error {
				q := req.URL.Query()

				if *names {
					q.Add(runtime.RevealParam, "1")
				}

				req.URL.RawQuery = q.Encode()
				return nil
			}))
			if err != nil {
				tw.Flush()
				fmt.Fprintf(errOut, "list: %v\n", err)
				return 1
			}

			for _, i := range sourceList.Items {
				fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%d/%d/%s\t%s\t%s\t%s\n", i.ID, i.Kind, i.Target, i.Filters,
					i.State, i.MemberWorks, i.FetchedWorks, total(i.TotalWorks.Value), i.Backfill.Value.Order.Value, when(time.Time(i.NextPollAt.Value)), dash(i.LastError.Value))
			}

			if len(sourceList.Items) < perPage || offset+perPage >= sourceList.Total {
				break
			}
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

		refreshSourceRequest := &apiclient.SourceRefreshRequest{}
		if *full {
			refreshSourceRequest.Full = apiclient.NewOptBool(true)
		}
		res, err := a.apiClient.RefreshSource(ctx, refreshSourceRequest, apiclient.RefreshSourceParams{
			ID: apiclient.ID(int(id)),
		})
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

		sourceSettingsRequest := &apiclient.SourceSettingsRequest{}

		if *poll != 0 {
			sourceSettingsRequest.PollInterval = apiclient.NewOptDuration(apiclient.Duration(*poll))
		}

		if *prio != 0 {
			sourceSettingsRequest.Priority = apiclient.NewOptNilInt(*prio)
		}

		if err := a.apiClient.SetSourceSettings(ctx, sourceSettingsRequest, apiclient.SetSourceSettingsParams{
			ID: apiclient.ID(int(id)),
		}); err != nil {
			fmt.Fprintf(errOut, "set %d: %v\n", id, err)
			return 1
		}

		resp, err := a.apiClient.GetSource(ctx, apiclient.GetSourceParams{
			ID: apiclient.ID(int(id)),
		})
		if err != nil {
			fmt.Fprintf(errOut, "get after set %d: %v\n", id, err)
			return 1
		}

		fmt.Fprintf(out, "source %d: poll every %s, priority %d, next poll %s\n", id, time.Duration(resp.PollInterval), resp.Priority, when(time.Time(resp.NextPollAt.Value)))
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
			err, msg = a.apiClient.ConfirmSource(ctx, apiclient.ConfirmSourceParams{ID: apiclient.ID(int(id))}), "confirmed (active)"
		case "pause":
			err, msg = a.apiClient.PauseSource(ctx, apiclient.PauseSourceParams{ID: apiclient.ID(int(id))}), "paused"
		case "resume":
			err, msg = a.apiClient.ResumeSource(ctx, apiclient.ResumeSourceParams{ID: apiclient.ID(int(id))}), "resumed (active)"
		case "cancel":
			var resp *apiclient.SourceCancelled
			resp, err = a.apiClient.CancelSource(ctx, apiclient.CancelSourceParams{ID: apiclient.ID(int(id))})

			msg = fmt.Sprintf("cancelled (%d queued jobs dropped; mirrored works kept)", resp.DroppedJobs)
		case "remove":
			err, msg = a.apiClient.RemoveSource(ctx, apiclient.RemoveSourceParams{ID: apiclient.ID(int(id))}), "removed (mirrored works kept)"
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

func target(r model.Source, names bool) string {
	if names || r.Kind == string(ao3url.KindWork) || r.Kind == string(ao3url.KindSeries) {
		return r.Target // ids are not names
	}
	return httpx.Hash(r.Target)
}

func filtersSummary(r model.Source, names bool) string {
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
