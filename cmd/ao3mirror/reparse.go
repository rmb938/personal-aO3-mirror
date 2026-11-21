package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
)

// runReparse rebuilds parsed data from stored raw pages. It makes no network requests: ctl is
// built without an AO3 client.
func runReparse(ctx context.Context, ctl *controllers.Controllers, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("reparse", flag.ContinueOnError)
	fs.SetOutput(errOut)
	all := fs.Bool("all", false, "reparse every stored work, not only those parsed by an older parser version")
	work := fs.Int64("work", 0, "reparse only this work id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	res, err := ctl.Reparse(ctx, controllers.ReparseOptions{
		All: *all, WorkID: *work,
		Progress: func(r controllers.ReparseResult) {
			fmt.Fprintf(errOut, "... seen %d, reparsed %d, skipped %d, failed %d\n", r.Seen, r.Reparsed, r.Skipped, r.Failed)
		},
	})
	if err != nil {
		fmt.Fprintf(errOut, "reparse: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "reparse done (0 requests): seen %d, reparsed %d, skipped %d (current parser), failed %d\n",
		res.Seen, res.Reparsed, res.Skipped, res.Failed)
	if res.ImagesQueued > 0 {
		fmt.Fprintf(out, "queued %d new images (the running daemon fetches them)\n", res.ImagesQueued)
	}
	if res.Failed > 0 {
		fmt.Fprintln(out, "failed pages were saved as skeletons under data/debug/ (see README \"Parser skeletons\")")
		return 1
	}
	return 0
}
