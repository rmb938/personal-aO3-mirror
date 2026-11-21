package store

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// openAt creates a meta.db under dir migrated only up to version upTo, for upgrade tests.
// The caller closes s.meta.
func openAt(t *testing.T, dir string, upTo int) *Store {
	t.Helper()
	ctx := context.Background()
	db, err := openDB(ctx, filepath.Join(dir, MetaFile), 0)
	require.NoError(t, err)
	s := &Store{core: &core{log: slog.Default(), now: time.Now}, meta: db}
	require.NoError(t, s.migrateTo(ctx, db, "meta", upTo))
	v, err := s.schemaVersion(ctx, "meta")
	require.NoError(t, err)
	require.Equal(t, upTo, v)
	return s
}

// 0003 turns text job targets into integers and keeps every job event.
func TestMigrateJobTargetInt(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	old := openAt(t, dir, 2)
	_, err := old.meta.ExecContext(ctx, `INSERT INTO jobs (id, kind, target, priority_class, dedupe_key, created_at, updated_at)
		VALUES (1, 'work', '12', 3, 'work:12', 1, 1), (2, 'poll', '1', 4, 'poll:5', 1, 1)`)
	require.NoError(t, err)
	_, err = old.meta.ExecContext(ctx, `INSERT INTO job_events (job_id, ts, event, detail) VALUES
		(1, 1, 'start', '{"attempt":1,"target":"12"}'), (1, 2, 'done', '{"ms":5,"words":3}'), (2, 1, 'start', '{"attempt":1,"target":"1"}')`)
	require.NoError(t, err)
	require.NoError(t, old.meta.Close())

	s, err := Open(ctx, dir, Options{})
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	tx := s.AutoCommitForTestOnly()
	j, err := tx.GetJob(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, int64(12), j.Target)
	var typ string
	require.NoError(t, s.meta.QueryRowContext(ctx, `SELECT typeof(target) FROM jobs WHERE id = 2`).Scan(&typ))
	require.Equal(t, "integer", typ)

	ev, err := tx.JobEvents(ctx, 1)
	require.NoError(t, err)
	require.Len(t, ev, 2)
	require.JSONEq(t, `{"attempt":1,"target":12}`, ev[0].Detail, "start target is a number")
	require.JSONEq(t, `{"ms":5,"words":3}`, ev[1].Detail, "other events unchanged")

	// The rebuilt tables keep their constraints: a text target is refused, and deleting a job
	// still deletes its events.
	_, err = s.meta.ExecContext(ctx, `INSERT INTO jobs (kind, target, priority_class, dedupe_key, created_at, updated_at)
		VALUES ('work', 'x', 3, 'work:x', 1, 1)`)
	require.ErrorContains(t, err, "CHECK constraint failed")
	_, err = s.meta.ExecContext(ctx, `DELETE FROM jobs WHERE id = 1`)
	require.NoError(t, err)
	var n int
	require.NoError(t, s.meta.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_events WHERE job_id = 1`).Scan(&n))
	require.Zero(t, n, "events cascade with their job")
	require.NoError(t, s.meta.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_events`).Scan(&n))
	require.Equal(t, 1, n, "other job's event kept")
}

// A stored target that is not an integer fails the migration instead of becoming 0.
func TestMigrateJobTargetIntRefusesText(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	old := openAt(t, dir, 2)
	_, err := old.meta.ExecContext(ctx, `INSERT INTO jobs (kind, target, priority_class, dedupe_key, created_at, updated_at)
		VALUES ('work', '/works/12', 3, 'work:12', 1, 1)`)
	require.NoError(t, err)
	require.NoError(t, old.meta.Close())

	_, err = Open(ctx, dir, Options{})
	require.ErrorContains(t, err, "0003_job_target_int.sql")
	require.ErrorContains(t, err, "CHECK constraint failed")
}
