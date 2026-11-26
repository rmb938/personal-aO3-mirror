package store

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/dirs/dirstest"
)

// rootAt creates dir (0700) when missing and opens it as a root for the test.
func rootAt(tb testing.TB, dir string) *os.Root {
	tb.Helper()
	return dirstest.At(tb, dir, 0o700)
}

// openAt creates a meta.db under dir migrated only up to version upTo, for upgrade tests.
// The caller closes s.meta.
func openAt(t *testing.T, dir string, upTo int) *Store {
	t.Helper()
	ctx := context.Background()
	db, err := openDB(ctx, rootAt(t, dir), MetaFile, dbOptions{})
	require.NoError(t, err)
	s := &Store{core: &core{log: slog.Default(), now: time.Now}, meta: db}
	require.NoError(t, s.migrateTo(ctx, db, "meta", upTo))
	v, err := s.schemaVersion(ctx, "meta")
	require.NoError(t, err)
	require.Equal(t, upTo, v)
	return s
}

// Every Go step names a migration file (a misspelt key would never run).
func TestMigrationStepsNameFiles(t *testing.T) {
	for key := range migrationSteps {
		_, err := migrationsFS.ReadFile("migrations/" + key)
		require.NoError(t, err, key)
	}
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

	s, err := Open(ctx, rootAt(t, dir), Options{})
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

// 0009 types the Unknown parents of fandoms as Media, and no other tag.
func TestMigrateMediaParents(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	old := openAt(t, dir, 8)
	_, err := old.meta.ExecContext(ctx, `INSERT INTO tags (id, name, type, first_seen) VALUES
		(1, 'Fixture Fandom', 'Fandom', 1), (2, 'Fixture Media', 'Unknown', 1), (3, 'Fixture Character', 'Character', 1),
		(4, 'Fixture Parent', 'Unknown', 1), (5, 'Fixture Lone', 'Unknown', 1)`)
	require.NoError(t, err)
	_, err = old.meta.ExecContext(ctx, `INSERT INTO tag_parents (child_id, parent_id) VALUES (1, 2), (3, 1), (3, 4)`)
	require.NoError(t, err)
	require.NoError(t, old.meta.Close())

	s, err := Open(ctx, rootAt(t, dir), Options{})
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	got := map[int64]string{}
	rows, err := s.meta.QueryContext(ctx, `SELECT id, type FROM tags`)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	for rows.Next() {
		var id int64
		var typ string
		require.NoError(t, rows.Scan(&id, &typ))
		got[id] = typ
	}
	require.NoError(t, rows.Err())
	require.Equal(t, map[int64]string{1: "Fandom", 2: "Media", 3: "Character", 4: "Unknown", 5: "Unknown"}, got,
		"only the fandom's parent becomes Media")
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

	_, err = Open(ctx, rootAt(t, dir), Options{})
	require.ErrorContains(t, err, "0003_job_target_int.sql")
	require.ErrorContains(t, err, "CHECK constraint failed")
}
