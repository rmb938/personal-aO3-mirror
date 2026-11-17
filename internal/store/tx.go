package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// querier is what query code runs against: a *sql.Tx inside InTx/InReadTx (or a *sql.DB for
// the autocommit test helper).
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ReadTx is a read-only transaction over both databases. It has every read method.
//
// It is a snapshot: in WAL mode a read-only transaction begins with a plain deferred BEGIN (the
// driver skips the DSN's _txlock=immediate for ReadOnly), so it takes no write lock and never
// blocks writers.
type ReadTx struct {
	*core
	meta    querier
	content querier
}

// Tx is a write transaction over both databases: every read method (via ReadTx) plus every
// write method. It holds both write locks (BEGIN IMMEDIATE) until it ends, so keep it short and
// never make a network request inside one.
type Tx struct {
	ReadTx
}

// InReadTx runs fn in one read-only transaction.
func (s *Store) InReadTx(ctx context.Context, fn func(ctx context.Context, tx *ReadTx) error) error {
	return s.guarded(ctx, false, func(ctx context.Context, tx *Tx) error { return fn(ctx, &tx.ReadTx) })
}

// InTx runs fn in one write transaction. It commits when fn returns nil and rolls back when fn
// returns an error or panics (the panic continues).
//
// The two databases are separate files and cannot commit atomically together. content.db
// commits first, then meta.db: a work's raw page and bodies land before the metadata that marks
// it fetched, so a failure between the two commits leaves the work to be fetched again.
func (s *Store) InTx(ctx context.Context, fn func(ctx context.Context, tx *Tx) error) error {
	return s.guarded(ctx, true, fn)
}

// guarded refuses a nested transaction and hands fn a ctx marked "in a transaction", which
// NotInTx (called before every AO3 fetch) and a nested InTx check.
func (s *Store) guarded(ctx context.Context, write bool, fn func(ctx context.Context, tx *Tx) error) error {
	if inTx(ctx) {
		return misuse("nested transaction (call the tx-taking helper, not the exported controller)")
	}
	return s.run(context.WithValue(ctx, inTxKey{}, true), write, fn)
}

// ReadValue is InReadTx for a function returning one value.
func ReadValue[T any](ctx context.Context, s *Store, fn func(ctx context.Context, tx *ReadTx) (T, error)) (T, error) {
	var v T
	err := s.InReadTx(ctx, func(ctx context.Context, tx *ReadTx) error {
		var err error
		v, err = fn(ctx, tx)
		return err
	})
	return v, err
}

// WriteValue is InTx for a function returning one value.
func WriteValue[T any](ctx context.Context, s *Store, fn func(ctx context.Context, tx *Tx) (T, error)) (T, error) {
	var v T
	err := s.InTx(ctx, func(ctx context.Context, tx *Tx) error {
		var err error
		v, err = fn(ctx, tx)
		return err
	})
	return v, err
}

// run begins a transaction on both databases (always meta first, so two writers can never wait
// on each other in opposite orders), runs fn, and commits or rolls back.
func (s *Store) run(ctx context.Context, write bool, fn func(ctx context.Context, tx *Tx) error) (err error) {
	opts := &sql.TxOptions{ReadOnly: !write}
	meta, err := s.meta.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("store: begin meta: %w", err)
	}
	content, err := s.content.BeginTx(ctx, opts)
	if err != nil {
		return errors.Join(fmt.Errorf("store: begin content: %w", err), rollback(meta))
	}
	done := false
	defer func() {
		if !done { // fn panicked: roll back, then let the panic continue
			_ = rollback(content)
			_ = rollback(meta)
		}
	}()
	ferr := fn(ctx, &Tx{ReadTx{core: s.core, meta: meta, content: content}})
	done = true
	if ferr != nil {
		return errors.Join(ferr, rollback(content), rollback(meta))
	}
	if err := content.Commit(); err != nil {
		return errors.Join(fmt.Errorf("store: commit content: %w", err), rollback(meta))
	}
	if err := meta.Commit(); err != nil {
		return fmt.Errorf("store: commit meta: %w", err)
	}
	return nil
}

// rollback rolls tx back, ignoring ErrTxDone (database/sql already rolled it back because its
// context ended).
func rollback(tx *sql.Tx) error {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return err
	}
	return nil
}

// AutoCommitForTestOnly returns a Tx whose statements run straight on the databases, each in its
// own implicit transaction. It exists so tests can seed and inspect state in one line; production
// code must use InTx / InReadTx.
func (s *Store) AutoCommitForTestOnly() *Tx {
	return &Tx{ReadTx{core: s.core, meta: s.meta, content: s.content}}
}
