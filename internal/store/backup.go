package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
)

// VacuumInto writes a consistent, compacted copy of users.db to path (docs/part3-key-rotation.md
// "Backup"). path must not exist or must be an empty file; the caller creates it 0600 first.
func (u *Users) VacuumInto(ctx context.Context, path string) error {
	return vacuumInto(ctx, u.db, path)
}

// VacuumInto writes a consistent, compacted copy of keys.db to path, as Users.VacuumInto does.
func (k *Keys) VacuumInto(ctx context.Context, path string) error { return vacuumInto(ctx, k.db, path) }

// vacuumInto runs VACUUM INTO, the one store statement outside InTx/InReadTx: SQLite refuses
// VACUUM inside a transaction. It reads one snapshot of db (as a read transaction would) and
// copies no free pages, so nothing deleted earlier is carried over. It still refuses a
// transaction's ctx, like every other store entry point.
func vacuumInto(ctx context.Context, db *sql.DB, path string) error {
	if inTx(ctx) {
		return misuse("VACUUM INTO inside a transaction")
	}
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("store: vacuum into %s: %w", filepath.Base(path), err)
	}
	return nil
}
