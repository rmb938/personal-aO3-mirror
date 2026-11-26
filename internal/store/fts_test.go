package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// ftsRows returns every works_fts row by work id.
func ftsRows(t *testing.T, db *sql.DB) map[int64]ftsRow {
	t.Helper()
	out := map[int64]ftsRow{}
	require.NoError(t, scanRows(context.Background(), db,
		`SELECT rowid, title, summary, creators, tags, series FROM works_fts`, nil, func(rows *sql.Rows) error {
			var id int64
			var r ftsRow
			if err := rows.Scan(&id, &r.title, &r.summary, &r.creators, &r.tags, &r.series); err != nil {
				return err
			}
			out[id] = r
			return nil
		}))
	return out
}

func TestFTSRow(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	w := fixtureWork(1)
	w.Creators = append(w.Creators, model.Creator{User: "fixture_other", Pseud: "other_pseud"})
	w.Series = append(w.Series, model.SeriesPart{ID: 78, Title: "Fixture Saga", Position: 2})
	_, err := s.UpsertWork(ctx, w, model.FromPage, 1)
	require.NoError(t, err)
	require.Equal(t, map[int64]ftsRow{1: {
		title:    "Fixture Title Alpha",
		summary:  "Fixture summary with lorem ipsum",
		creators: "fixture_author fixture_pseud fixture_other other_pseud",
		tags:     "Fixture Rating • Fixture Fandom • Fixture Character • fixture freeform",
		series:   "Fixture Series • Fixture Saga",
	}}, ftsRows(t, s.meta))
	// Rebuilding an unchanged work rewrites nothing (the row keeps its FTS5 rowid slot).
	// total_changes() is per connection, so pin one.
	conn, err := s.meta.Conn(ctx)
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close()) }()
	var changes, after int
	require.NoError(t, conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&changes))
	require.NoError(t, updateFTS(ctx, conn, `VALUES (1)`))
	require.NoError(t, conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&after))
	require.Equal(t, changes, after, "no writes for an unchanged work")
	// A changed row is rewritten (the check above can see writes).
	_, err = conn.ExecContext(ctx, `UPDATE works SET title = 'Fixture Renamed' WHERE id = 1`)
	require.NoError(t, err)
	require.NoError(t, conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&changes))
	require.NoError(t, updateFTS(ctx, conn, `VALUES (1)`))
	require.NoError(t, conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&after))
	require.Greater(t, after, changes)
	require.Equal(t, "Fixture Renamed", ftsRows(t, s.meta)[1].title)
}

func TestFTSSeriesTitles(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	search := func(q string) []int64 {
		t.Helper()
		page, err := s.FindWorks(ctx, model.WorkFilter{Query: q, Sort: model.WorkSortRevised, Direction: model.SortAsc,
			Limit: 10})
		require.NoError(t, err)
		return workIDs(page)
	}
	for id := range int64(3) {
		w := fixtureWork(id + 1)
		w.Revised = "2026-11-0" + string(rune('1'+id))
		w.Series = []model.SeriesPart{{ID: 77, Title: "Fixture Series", Position: int(id) + 1}}
		if id == 2 {
			w.Series = nil
		}
		_, err := s.UpsertWork(ctx, w, model.FromPage, 1)
		require.NoError(t, err)
	}
	require.Equal(t, []int64{1, 2}, search("series"))

	// Another work's blurb renames the series: every work in it is reindexed.
	w := fixtureWork(2)
	w.Revised = "2026-11-02"
	w.Series = []model.SeriesPart{{ID: 77, Title: "Fixture Lorem Saga", Position: 2}}
	_, err := s.UpsertWork(ctx, w, model.FromBlurb, 1)
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2}, search("saga"))
	require.Empty(t, search(`"fixture series"`))

	// An empty title (a blurb without it) keeps the stored one.
	w.Series[0].Title = ""
	_, err = s.UpsertWork(ctx, w, model.FromBlurb, 1)
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2}, search("saga"))

	// So does a series page.
	require.NoError(t, s.UpsertSeries(ctx, model.Series{ID: 77, Title: "Fixture Chronicle", WorksCount: 3}))
	require.Equal(t, []int64{1, 2}, search("chronicle"))
	require.Empty(t, search("saga"))

	// Series membership from a series page: work 3 joins, then leaves. Fetched works keep theirs.
	w4 := fixtureWork(4) // blurb only, not in the series
	w4.Revised, w4.Series = "2026-11-04", nil
	_, err = s.UpsertWork(ctx, w4, model.FromBlurb, 1)
	require.NoError(t, err)
	require.NoError(t, s.SetSeriesMembers(ctx, 77, []SeriesMember{{WorkID: 4, Position: 3}}))
	require.Equal(t, []int64{1, 2, 4}, search("chronicle"))
	require.NoError(t, s.SetSeriesMembers(ctx, 77, nil))
	require.Equal(t, []int64{1, 2}, search("chronicle"))
}

// BenchmarkFTS rebuilds search index rows over seedWorks's works (synthetic titles, four filler
// tags each, one creator, every 10th work in one of 1000 series): the 0008 backfill (into an
// empty index, rolled back), and rebuilding every work and one work (an upsert) with the index
// already current, so only the reads and comparison are timed.
//
//	go test ./internal/store -run '^$' -bench 'FTS' -benchtime 5x
func BenchmarkFTS(b *testing.B) {
	s := seedWorks(b)
	ctx := context.Background()
	_, err := s.meta.ExecContext(ctx, `
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 1000)
		INSERT INTO creators (id, user_login, pseud) SELECT i, 'fixture_user_' || i, 'Fixture Pseud ' || i FROM n;
		INSERT INTO work_creators (work_id, creator_id, position) SELECT id, 1 + id % 1000, 0 FROM works;
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 1000)
		INSERT INTO series (id, title, first_seen) SELECT i, 'Fixture Series ' || i, 1 FROM n;
		INSERT INTO series_works (series_id, work_id, position)
			SELECT 1 + id % 1000, id, id / 1000 FROM works WHERE id % 10 = 0;`)
	require.NoError(b, err)
	b.Run("backfill", func(b *testing.B) {
		for b.Loop() {
			tx, err := s.meta.BeginTx(ctx, nil)
			require.NoError(b, err)
			_, err = tx.ExecContext(ctx, `DELETE FROM works_fts`)
			require.NoError(b, err)
			require.NoError(b, backfillFTS(ctx, tx))
			require.NoError(b, tx.Rollback())
		}
	})
	require.NoError(b, backfillFTS(ctx, s.meta))
	for _, bc := range []struct {
		name string
		sel  string
		args []any
	}{
		{"current/all", `SELECT id FROM works`, nil},
		{"current/work", `VALUES (?)`, []any{3000}},
		{"current/series", seriesWorks(`?`), []any{1}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			for b.Loop() {
				require.NoError(b, updateFTS(ctx, s.meta, bc.sel, bc.args...))
			}
		})
	}
}

// 0008 rebuilds works_fts with a series column and indexes every work (its Go step).
func TestMigrateWorksFTSSeries(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	old := openAt(t, dir, 7)
	_, err := old.meta.ExecContext(ctx, `
		INSERT INTO works (id, title, summary_html, first_seen) VALUES
			(1, 'Fixture Title', '<p>Fixture <b>lorem</b> &amp; ipsum</p>', 1), (2, 'Fixture Other', '', 1);
		INSERT INTO creators (id, user_login, pseud) VALUES (1, 'fixture_author', 'fixture_pseud');
		INSERT INTO work_creators (work_id, creator_id, position) VALUES (1, 1, 0);
		INSERT INTO tags (id, name, type, first_seen) VALUES (1, 'Fixture Tag', 'Freeform', 1);
		INSERT INTO work_tags (work_id, tag_id, position) VALUES (1, 1, 0);
		INSERT INTO series (id, title, first_seen) VALUES (77, 'Fixture Series', 1);
		INSERT INTO series_works (series_id, work_id, position) VALUES (77, 1, 1), (77, 9, 2);
		INSERT INTO works_fts (rowid, title, summary, creators, tags) VALUES (1, 'stale', '', '', '');`)
	require.NoError(t, err)
	require.NoError(t, old.meta.Close())

	s, err := Open(ctx, rootAt(t, dir), Options{})
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	require.Equal(t, map[int64]ftsRow{
		1: {title: "Fixture Title", summary: "Fixture lorem & ipsum", creators: "fixture_author fixture_pseud",
			tags: "Fixture Tag", series: "Fixture Series"},
		2: {title: "Fixture Other"},
	}, ftsRows(t, s.meta))
}
