package main

import (
	"context"
	"fmt"
	"io"

	"github.com/rmb938/personal-aO3-mirror/internal/apiclient"
)

const backupUsage = `usage: ao3mirror backup NAME

Copies users.db and keys.db (accounts and the master keys that encrypt them) into NAME, a new
directory (0700, files 0600) under api.backup_dir (data/backups/ by default; serve creates it
0700). NAME is letters, digits, '.', '_' and '-', not starting with '.', and must not exist yet.
The copies are as sensitive as the live files: keep them private, and move them off the
machine yourself if you want an off-site copy.

To restore: stop 'ao3mirror serve', copy both files into the admin socket's directory
(data/api/ by default; keep mode 0600 and delete any users.db-wal and users.db-shm left there)
and start it again. meta.db and content.db are not part of a backup.
` + adminSocketNote

// runBackup asks serve for a backup named by args' one argument, under api.backup_dir.
func runBackup(ctx context.Context, a *app, args []string, out, errOut io.Writer) int {
	if len(args) != 1 || args[0] == "" || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(errOut, backupUsage)
		return 2
	}
	name := args[0]
	if err := a.client.Backup(ctx, &apiclient.BackupRequest{Name: name}); err != nil {
		return fail(errOut, "backup", err)
	}
	fmt.Fprintf(out, "backup written to %s\n", a.cfg.Path(a.cfg.API.BackupDir, name))
	return 0
}
