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

	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

var (
	// ErrBackupExists is a backup directory that already exists: a backup never writes into
	// or over an existing one.
	ErrBackupExists = errors.New("controllers: the backup directory already exists")
	// ErrNoBackupParent is a backup directory whose parent doesn't exist.
	ErrNoBackupParent = errors.New("controllers: the backup directory's parent does not exist")
)

// BackupDatabases snapshots users.db and keys.db into dir (docs/part3-key-rotation.md
// "Backup"). It holds the rotation mutex, so keys.db can't change between the two copies:
// every value in the users.db copy is under a key the keys.db copy holds. dir must be absolute
// and not exist, and its parent must exist. It is created 0700 with both copies 0600 in it; on
// failure it is removed again.
func (m *MasterKeys) BackupDatabases(ctx context.Context, dir string) (err error) {
	if !filepath.IsAbs(dir) {
		return errors.New("controllers: the backup directory must be an absolute path")
	}
	if err := store.NotInTx(ctx, "backup"); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	start := time.Now()
	if err := os.Mkdir(dir, 0o700); err != nil {
		switch {
		case errors.Is(err, fs.ErrExist):
			return ErrBackupExists
		case errors.Is(err, fs.ErrNotExist):
			return ErrNoBackupParent
		}
		return fmt.Errorf("controllers: create the backup directory: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(dir))
		}
	}()
	usersBytes, err := snapshotDB(ctx, filepath.Join(dir, store.UsersFile), m.users.VacuumInto)
	if err != nil {
		return err
	}
	keysBytes, err := snapshotDB(ctx, filepath.Join(dir, store.KeysFile), m.keys.VacuumInto)
	if err != nil {
		return err
	}
	if err := syncPath(dir); err != nil {
		return err
	}
	// No path in the log: it comes from the request body (CLAUDE.md "Logging rules").
	m.event(ctx, slog.LevelInfo, "backup_created", "users_bytes", usersBytes, "keys_bytes", keysBytes,
		"ms", time.Since(start).Milliseconds())
	return nil
}

// snapshotDB creates path 0600 (VACUUM INTO keeps an empty file's mode; a file it created
// itself would get the umask's), runs vacuum into it and syncs it to disk (VACUUM INTO writes
// its output unsynced). It returns the copy's size.
func snapshotDB(ctx context.Context, path string, vacuum func(context.Context, string) error) (int64, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, fmt.Errorf("controllers: create %s: %w", filepath.Base(path), err)
	}
	if err := f.Close(); err != nil {
		return 0, fmt.Errorf("controllers: create %s: %w", filepath.Base(path), err)
	}
	if err := vacuum(ctx, path); err != nil {
		return 0, err
	}
	if err := syncPath(path); err != nil {
		return 0, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("controllers: stat %s: %w", filepath.Base(path), err)
	}
	return fi.Size(), nil
}

// syncPath fsyncs a file or directory.
func syncPath(path string) (err error) {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("controllers: sync %s: %w", filepath.Base(path), err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("controllers: sync %s: %w", filepath.Base(path), err)
	}
	return nil
}
