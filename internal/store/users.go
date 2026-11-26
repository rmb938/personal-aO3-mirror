package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// UsersFile is the accounts database's file name, in the API's private directory.
const UsersFile = "users.db"

// Users is users.db, the API's accounts (docs/part2-api.md stage 16). It is a separate database
// with its own handle: `run` never opens it. Like Store it has no query methods: all access
// goes through InTx / InReadTx (*UsersTx / *UsersReadTx), under the same txguard rules (no
// nesting, with a Store transaction either).
type Users struct {
	*core
	db *sql.DB
}

// UsersReadTx is a read-only transaction on users.db. It has every read method.
type UsersReadTx struct {
	*core
	q querier
}

// UsersTx is a write transaction on users.db: every read method plus every write method.
type UsersTx struct {
	UsersReadTx
}

// OpenUsers opens (creating if needed) users.db in dir, the API's private directory opened as
// an os.Root (dirs.PrivateSub), and applies its migrations. The database and its -wal/-shm
// files are created 0600, and an existing one readable by group or others is refused (it holds
// password hashes and signing keys).
func OpenUsers(ctx context.Context, dir *os.Root, o Options) (*Users, error) {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	for _, name := range []string{UsersFile, UsersFile + "-wal", UsersFile + "-shm"} {
		if err := createPrivate(dir, name); err != nil {
			return nil, err
		}
	}
	db, err := openDB(ctx, dir, UsersFile, dbOptions{})
	if err != nil {
		return nil, err
	}
	u := &Users{core: &core{log: o.Logger.With("component", "store"), now: o.Now}, db: db}
	if err := u.migrate(ctx, db, "users"); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return u, nil
}

// createPrivate creates file name in dir 0600 if it doesn't exist, and refuses it if it does
// with group or other permission bits.
func createPrivate(dir *os.Root, name string) error {
	f, err := dir.OpenFile(name, os.O_RDONLY|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("store: create %s: %w", name, err)
	}
	fi, err := f.Stat()
	if err = errors.Join(err, f.Close()); err != nil {
		return fmt.Errorf("store: stat %s: %w", name, err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("store: %s has mode %04o; it must not be readable by group or others (chmod 600)",
			name, fi.Mode().Perm())
	}
	return nil
}

// Close closes users.db.
func (u *Users) Close() error { return u.db.Close() }

// InReadTx runs fn in one read-only transaction (a snapshot that never blocks writers).
func (u *Users) InReadTx(ctx context.Context, fn func(ctx context.Context, tx *UsersReadTx) error) error {
	return u.guarded(ctx, false, func(ctx context.Context, tx *UsersTx) error { return fn(ctx, &tx.UsersReadTx) })
}

// InTx runs fn in one write transaction. It commits when fn returns nil and rolls back when fn
// returns an error or panics (the panic continues). It holds the write lock until it ends, so
// keep it short: no password hashing inside one.
func (u *Users) InTx(ctx context.Context, fn func(ctx context.Context, tx *UsersTx) error) error {
	return u.guarded(ctx, true, fn)
}

// ReadUsersValue is Users.InReadTx for a function returning one value.
func ReadUsersValue[T any](ctx context.Context, u *Users, fn func(ctx context.Context, tx *UsersReadTx) (T, error)) (T, error) {
	var v T
	err := u.InReadTx(ctx, func(ctx context.Context, tx *UsersReadTx) error {
		var err error
		v, err = fn(ctx, tx)
		return err
	})
	return v, err
}

// WriteUsersValue is Users.InTx for a function returning one value.
func WriteUsersValue[T any](ctx context.Context, u *Users, fn func(ctx context.Context, tx *UsersTx) (T, error)) (T, error) {
	var v T
	err := u.InTx(ctx, func(ctx context.Context, tx *UsersTx) error {
		var err error
		v, err = fn(ctx, tx)
		return err
	})
	return v, err
}

func (u *Users) guarded(ctx context.Context, write bool, fn func(ctx context.Context, tx *UsersTx) error) (err error) {
	if inTx(ctx) {
		return misuse("nested transaction (call the tx-taking helper, not the exported controller)")
	}
	ctx = context.WithValue(ctx, inTxKey{}, true)
	tx, err := u.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: !write})
	if err != nil {
		return fmt.Errorf("store: begin users: %w", err)
	}
	done := false
	defer func() {
		if !done { // fn panicked: roll back, then let the panic continue
			_ = rollback(tx)
		}
	}()
	ferr := fn(ctx, &UsersTx{UsersReadTx{core: u.core, q: tx}})
	done = true
	if ferr != nil {
		return errors.Join(ferr, rollback(tx))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit users: %w", err)
	}
	return nil
}

// AutoCommitForTestOnly returns a UsersTx whose statements run straight on the database, each
// in its own implicit transaction. Tests only.
func (u *Users) AutoCommitForTestOnly() *UsersTx {
	return &UsersTx{UsersReadTx{core: u.core, q: u.db}}
}
