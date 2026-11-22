package store

import (
	"context"
	"errors"
	"fmt"
)

// ErrTxMisuse reports a transaction used against the rules below. It is a programming error:
// the tests run every controller path, so it should only ever show up there.
//
//   - No nesting: a transaction is never opened inside another (controllers call each other's
//     tx-taking helpers instead of their exported methods). Without this check a nested
//     BEGIN IMMEDIATE would wait on its own outer transaction's write lock until busy_timeout.
//   - No AO3 fetch inside a transaction: it would hold the SQLite write lock across a paced
//     request that can wait for minutes, stalling the daemon and the CLI.
//   - No password hashing inside a transaction: an Argon2id hash takes tens of milliseconds
//     and can wait seconds for a hasher slot, holding the users.db write lock throughout.
//
// "One transaction per step" is a design rule (CLAUDE.md "Layering"), checked in review.
var ErrTxMisuse = errors.New("store: transaction misuse")

type inTxKey struct{}

func misuse(reason string) error { return fmt.Errorf("%w: %s", ErrTxMisuse, reason) }

// inTx reports whether ctx is the ctx of a running transaction's callback.
func inTx(ctx context.Context) bool {
	in, _ := ctx.Value(inTxKey{}).(bool)
	return in
}

// NotInTx fails if ctx belongs to a running transaction. The controllers call it before work
// that must never hold a transaction open: every AO3 request and every password hash. what
// names that work in the error ("AO3 fetch", "password hash").
func NotInTx(ctx context.Context, what string) error {
	if inTx(ctx) {
		return misuse(what + " inside a transaction")
	}
	return nil
}
