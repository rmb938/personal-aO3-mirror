package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/rmb938/personal-aO3-mirror/internal/apiclient"
)

const backupUsage = `usage: ao3mirror backup DIR

Copies users.db and keys.db (accounts and the master keys that encrypt them) into DIR, a new
directory (0700, files 0600). DIR must not exist; its parent must. A relative DIR is relative to
the current directory. The copies are as sensitive as the live files: keep them private.

To restore: stop 'ao3mirror serve', copy both files into the admin socket's directory
(data/api/ by default; keep mode 0600 and delete any users.db-wal and users.db-shm left there)
and start it again. meta.db and content.db are not part of a backup.
` + adminSocketNote

// runBackup asks serve for a backup into args' one directory, made absolute here because serve
// may run in another working directory.
func runBackup(ctx context.Context, a *app, args []string, out, errOut io.Writer) int {
	if len(args) != 1 || args[0] == "" || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(errOut, backupUsage)
		return 2
	}
	dir, err := filepath.Abs(args[0])
	if err != nil {
		fmt.Fprintf(errOut, "backup: %v\n", err)
		return 1
	}
	if err := a.client.Backup(ctx, &apiclient.BackupRequest{Dir: dir}); err != nil {
		return fail(errOut, "backup", err)
	}
	fmt.Fprintf(out, "backup written to %s\n", dir)
	return 0
}
