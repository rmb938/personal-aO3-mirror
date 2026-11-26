package controllers

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/dirs"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

var (
	// ErrBackupExists is a backup name already taken: a backup never writes into or over an
	// existing one.
	ErrBackupExists = errors.New("controllers: the backup directory already exists")
	// ErrBadBackupName is a backup name that is not one plain path component (ValidBackupName).
	ErrBadBackupName = errors.New("controllers: the backup name must be one plain path component")
)

// MaxBackupNameBytes bounds a backup name.
const MaxBackupNameBytes = 128

// ValidBackupName reports whether name may name a backup: 1..MaxBackupNameBytes bytes of
// [A-Za-z0-9._-], not starting with '.', so one plain component of the backup directory (no
// separator, no "." or "..", no hidden file).
func ValidBackupName(name string) bool {
	if name == "" || len(name) > MaxBackupNameBytes || name[0] == '.' {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// BackupDatabases snapshots users.db and keys.db into name in the backup directory
// (api.backup_dir; docs/part3-key-rotation.md "Backup"). It holds the rotation mutex, so
// keys.db can't change between the two copies: every value in the users.db copy is under a key
// the keys.db copy holds. name must pass ValidBackupName and not exist yet. The backup
// directory is refused when group or others can reach it; name is created 0700 with both copies
// 0600 in it, all through the directory's root, and removed again on failure.
func (m *MasterKeys) BackupDatabases(ctx context.Context, name string) (err error) {
	// filepath.IsLocal as well as ValidBackupName: the name comes from a request, and this is
	// where it becomes a path.
	if !ValidBackupName(name) || !filepath.IsLocal(name) || filepath.Base(name) != name {
		return ErrBadBackupName
	}
	if m.backupDir == nil {
		return errors.New("controllers: no backup directory configured")
	}
	if err := store.NotInTx(ctx, "backup"); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	start := time.Now()
	root := m.backupDir
	if err := dirs.CheckPrivate(root); err != nil {
		return fmt.Errorf("controllers: the backup directory: %w", err)
	}
	if err := root.Mkdir(name, 0o700); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrBackupExists
		}
		return fmt.Errorf("controllers: create the backup directory: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, root.RemoveAll(name))
		}
	}()
	usersBytes, err := snapshotDB(ctx, root, filepath.Join(name, store.UsersFile), m.users.VacuumInto)
	if err != nil {
		return err
	}
	keysBytes, err := snapshotDB(ctx, root, filepath.Join(name, store.KeysFile), m.keys.VacuumInto)
	if err != nil {
		return err
	}
	if err := syncFile(root, name); err != nil {
		return err
	}
	// No path in the log: it comes from the request body (CLAUDE.md "Logging rules").
	m.event(ctx, slog.LevelInfo, "backup_created", "users_bytes", usersBytes, "keys_bytes", keysBytes,
		"ms", time.Since(start).Milliseconds())
	return nil
}

// snapshotDB creates file name in root 0600 (VACUUM INTO keeps an empty file's mode; a file it
// created itself would get the umask's), runs vacuum into it and syncs it to disk (VACUUM INTO
// writes its output unsynced). It returns the copy's size. SQLite writes the copy by path: the
// root's name joined with name, the file just created inside the root.
func snapshotDB(ctx context.Context, root *os.Root, name string, vacuum func(context.Context, string) error) (int64, error) {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, fmt.Errorf("controllers: create %s: %w", filepath.Base(name), err)
	}
	if err := f.Close(); err != nil {
		return 0, fmt.Errorf("controllers: create %s: %w", filepath.Base(name), err)
	}
	if err := vacuum(ctx, filepath.Join(root.Name(), name)); err != nil {
		return 0, err
	}
	if err := syncFile(root, name); err != nil {
		return 0, err
	}
	fi, err := root.Stat(name)
	if err != nil {
		return 0, fmt.Errorf("controllers: stat %s: %w", filepath.Base(name), err)
	}
	return fi.Size(), nil
}

// syncFile fsyncs file or directory name in root.
func syncFile(root *os.Root, name string) (err error) {
	f, err := root.Open(name)
	if err != nil {
		return fmt.Errorf("controllers: sync %s: %w", filepath.Base(name), err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("controllers: sync %s: %w", filepath.Base(name), err)
	}
	return nil
}
