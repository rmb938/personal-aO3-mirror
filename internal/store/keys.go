package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// KeysFile is the master keys database's file name, in the API's private directory next to
// users.db.
const KeysFile = "keys.db"

// ErrNoMasterKey is a master key ID with no key in the state the operation needs.
var ErrNoMasterKey = errors.New("store: no such master key")

// Keys is keys.db, the master keys that encrypt every secret in users.db
// (docs/part3-key-rotation.md). It is a separate database from users.db, so a copy of users.db
// alone decrypts nothing; only `serve` opens it. Like Users it has no query methods: all access
// goes through InTx / InReadTx (*KeysTx / *KeysReadTx), under the same txguard rules.
//
// Losing keys.db loses every password and TOTP secret (each user then needs an admin reset
// link): back it up together with users.db, never one without the other.
//
// It runs in rollback-journal mode (not WAL) with secure_delete on, so a deleted key is
// overwritten rather than left in a free page or a WAL frame.
type Keys struct {
	*core
	db *sql.DB
}

// KeysReadTx is a read-only transaction on keys.db. It has every read method.
type KeysReadTx struct {
	*core
	q querier
}

// KeysTx is a write transaction on keys.db: every read method plus every write method.
type KeysTx struct {
	KeysReadTx
}

// OpenKeys opens (creating if needed) keys.db in dir, the API's private directory opened as an
// os.Root (dirs.PrivateSub), and applies its migrations. The database is created 0600 (SQLite
// gives its journal the same mode), and an existing one readable by group or others is refused.
func OpenKeys(ctx context.Context, dir *os.Root, o Options) (*Keys, error) {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if err := createPrivate(dir, KeysFile); err != nil {
		return nil, err
	}
	db, err := openDB(ctx, dir, KeysFile, dbOptions{rollback: true, pragmas: []string{"secure_delete(ON)"}})
	if err != nil {
		return nil, err
	}
	k := &Keys{core: &core{log: o.Logger.With("component", "store"), now: o.Now}, db: db}
	if err := k.migrate(ctx, db, "keys"); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return k, nil
}

// Close closes keys.db.
func (k *Keys) Close() error { return k.db.Close() }

// InReadTx runs fn in one read-only transaction.
func (k *Keys) InReadTx(ctx context.Context, fn func(ctx context.Context, tx *KeysReadTx) error) error {
	return k.guarded(ctx, false, func(ctx context.Context, tx *KeysTx) error { return fn(ctx, &tx.KeysReadTx) })
}

// InTx runs fn in one write transaction. It commits when fn returns nil and rolls back when fn
// returns an error or panics (the panic continues).
func (k *Keys) InTx(ctx context.Context, fn func(ctx context.Context, tx *KeysTx) error) error {
	return k.guarded(ctx, true, fn)
}

// ReadKeysValue is Keys.InReadTx for a function returning one value.
func ReadKeysValue[T any](ctx context.Context, k *Keys, fn func(ctx context.Context, tx *KeysReadTx) (T, error)) (T, error) {
	var v T
	err := k.InReadTx(ctx, func(ctx context.Context, tx *KeysReadTx) error {
		var err error
		v, err = fn(ctx, tx)
		return err
	})
	return v, err
}

// WriteKeysValue is Keys.InTx for a function returning one value.
func WriteKeysValue[T any](ctx context.Context, k *Keys, fn func(ctx context.Context, tx *KeysTx) (T, error)) (T, error) {
	var v T
	err := k.InTx(ctx, func(ctx context.Context, tx *KeysTx) error {
		var err error
		v, err = fn(ctx, tx)
		return err
	})
	return v, err
}

func (k *Keys) guarded(ctx context.Context, write bool, fn func(ctx context.Context, tx *KeysTx) error) (err error) {
	if inTx(ctx) {
		return misuse("nested transaction (call the tx-taking helper, not the exported controller)")
	}
	ctx = context.WithValue(ctx, inTxKey{}, true)
	tx, err := k.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: !write})
	if err != nil {
		return fmt.Errorf("store: begin keys: %w", err)
	}
	done := false
	defer func() {
		if !done { // fn panicked: roll back, then let the panic continue
			_ = rollback(tx)
		}
	}()
	ferr := fn(ctx, &KeysTx{KeysReadTx{core: k.core, q: tx}})
	done = true
	if ferr != nil {
		return errors.Join(ferr, rollback(tx))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit keys: %w", err)
	}
	return nil
}

// AutoCommitForTestOnly returns a KeysTx whose statements run straight on the database, each in
// its own implicit transaction. Tests only.
func (k *Keys) AutoCommitForTestOnly() *KeysTx {
	return &KeysTx{KeysReadTx{core: k.core, q: k.db}}
}

// MasterKeys returns every master key by ID, retired ones included.
func (tx *KeysReadTx) MasterKeys(ctx context.Context) (out []model.MasterKey, err error) {
	rows, err := tx.q.QueryContext(ctx, `SELECT id, key, created_at, retired_at FROM master_keys ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var mk model.MasterKey
		var created, retired int64
		if err := rows.Scan(&mk.ID, &mk.Key, &created, &retired); err != nil {
			return nil, err
		}
		mk.CreatedAt, mk.RetiredAt = unixTime(created), unixTime(retired)
		out = append(out, mk)
	}
	return out, rows.Err()
}

// InsertMasterKey stores key (32 bytes) as the active master key and returns its ID. It fails
// while another key is active (retire that one first).
func (tx *KeysTx) InsertMasterKey(ctx context.Context, key []byte, at time.Time) (int64, error) {
	res, err := tx.q.ExecContext(ctx, `INSERT INTO master_keys (key, created_at) VALUES (?,?)`, key, zeroUnix(at))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RetireMasterKey marks the active master key id retired at at. It fails with ErrNoMasterKey
// when id isn't the active key.
func (tx *KeysTx) RetireMasterKey(ctx context.Context, id int64, at time.Time) error {
	return execOne(ctx, tx.q, ErrNoMasterKey, `UPDATE master_keys SET retired_at = ? WHERE id = ? AND retired_at = 0`,
		zeroUnix(at), id)
}

// DeleteMasterKey deletes the retired master key id (secure_delete overwrites it). It fails
// with ErrNoMasterKey when id isn't a retired key: the active key is never deleted.
func (tx *KeysTx) DeleteMasterKey(ctx context.Context, id int64) error {
	return execOne(ctx, tx.q, ErrNoMasterKey, `DELETE FROM master_keys WHERE id = ? AND retired_at != 0`, id)
}
