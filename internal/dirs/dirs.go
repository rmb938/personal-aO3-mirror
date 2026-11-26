// Package dirs opens the mirror's directories as os.Root values. The data directory (config
// data_dir) is opened once, by its configured path, and must already exist; every file the
// program creates, reads or removes under it goes through that root or one opened inside it,
// so no "..", absolute name or symlink in a name can lead outside it.
package dirs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// ErrNoDataDir is a data directory that does not exist: the user creates it once.
var ErrNoDataDir = errors.New("the data directory does not exist")

// OpenData opens the data directory at path. It is never created here: a missing one is
// ErrNoDataDir, with the command that creates it.
func OpenData(path string) (*os.Root, error) {
	r, err := os.OpenRoot(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("data_dir %s: %w; create it first (mkdir -m 750 %s)", path, ErrNoDataDir, path)
	}
	if err != nil {
		return nil, fmt.Errorf("data_dir %s: %w", path, err)
	}
	return r, nil
}

// Sub creates name under parent (with perm, and any missing parents) when missing and opens it
// as a root. The caller closes it.
func Sub(parent *os.Root, name string, perm os.FileMode) (*os.Root, error) {
	if err := parent.MkdirAll(name, perm); err != nil {
		return nil, fmt.Errorf("create %s: %w", name, err)
	}
	r, err := parent.OpenRoot(name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	return r, nil
}

// PrivateSub is Sub with mode 0700 that refuses a directory group or others can reach: for the
// directories holding users.db, keys.db, the admin socket and backups.
func PrivateSub(parent *os.Root, name string) (*os.Root, error) {
	r, err := Sub(parent, name, 0o700)
	if err != nil {
		return nil, err
	}
	if err := CheckPrivate(r); err != nil {
		return nil, errors.Join(err, r.Close())
	}
	return r, nil
}

// CheckPrivate refuses a root directory that group or others can reach.
func CheckPrivate(r *os.Root) error {
	st, err := r.Stat(".")
	if err != nil {
		return fmt.Errorf("stat %s: %w", r.Name(), err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("directory %s has mode %s; it must be private to this user (chmod 700)", r.Name(), st.Mode().Perm())
	}
	return nil
}
