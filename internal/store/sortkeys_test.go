package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

func TestTitleSortKey(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "",
		"Fixture Title":          "fixture title",
		"The Fixture":            "fixture, the",
		"A Fixture":              "fixture, a",
		"An Fixture":             "fixture, an",
		"Anfixture":              "anfixture",
		"the":                    "the",
		"the ":                   ", the",
		"A The Fixture":          "fixture, a, the", // a/an first, then the
		"The A Fixture":          "a fixture, the",
		`"The Quoted Fixture"`:   `quoted fixture", the`,
		"''Twice":                "'twice", // one character only
		".fixture":               "fixture",
		"/fixture":               "fixture",
		"-fixture":               "-fixture", // not in AO3's title set
		"12":                     "00012",
		"1a":                     "0001a",
		"1é":                     "0001é", // padding counts characters, not bytes
		"123ab":                  "123ab",
		"7 Fixtures":             "7 fixtures",
		"'5":                     "00005", // stripped before the digit check
		"Ünïcode Fixture":        "ünïcode fixture",
		"Fixture, The":           "fixture, the",
		"Anonymous Fixture Name": "anonymous fixture name",
	} {
		require.Equal(t, want, titleSortKey(in), "%q", in)
	}
}

func TestCreatorSortKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		anon bool
		cs   []sortCreator
		want string
	}{
		{"none", false, nil, ""},
		{"anonymous", true, []sortCreator{{"fixture_pseud", "fixture_author"}}, "Anonymous"},
		{"one", false, []sortCreator{{"Fixture_Pseud", "fixture_author"}}, "fixture_pseud"},
		{"sorted by lowercased name", false,
			[]sortCreator{{"Zeta", "a"}, {"alpha", "z"}, {"Mid", "m"}}, "alpha,  mid,  zeta"},
		{"leading punctuation of the joined key only", false,
			[]sortCreator{{"_b", "x"}, {"a", "y"}}, "b,  a"},
		{"one punctuation character", false, []sortCreator{{"!!fixture", "x"}}, "!fixture"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := append([]sortCreator(nil), tc.cs...)
			require.Equal(t, tc.want, creatorSortKey(tc.anon, tc.cs))
			require.Equal(t, in, tc.cs, "input reordered")
		})
	}
}

// Upserts keep the keys current, and the title and creator sorts page by them (ascending by
// default, id breaking ties).
func TestFindWorksTitleCreatorSorts(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	for _, w := range []struct {
		id      int64
		title   string
		anon    bool
		pseuds  []string
		retitle string // title rewritten by a later upsert, if any
	}{
		{1, "The Fixture Zed", false, []string{"fixture_b"}, ""},
		{2, "Fixture Alpha", false, []string{"_fixture_c", "fixture_a"}, ""},
		{3, "A Fixture Mid", true, []string{"fixture_a"}, ""},
		{4, "Fixture Old", false, []string{"fixture_b"}, "10 Fixtures"},
	} {
		x := fixtureWork(w.id)
		x.Series = nil
		x.Title, x.Anonymous, x.Creators = w.title, w.anon, nil
		for _, p := range w.pseuds {
			x.Creators = append(x.Creators, model.Creator{User: "fixture_user_" + p, Pseud: p})
		}
		_, err := s.UpsertWork(ctx, x, model.FromBlurb, 1)
		require.NoError(t, err)
		if w.retitle != "" {
			x.Title = w.retitle
			_, err = s.UpsertWork(ctx, x, model.FromBlurb, 1)
			require.NoError(t, err)
		}
	}
	// title keys: 1 "fixture zed, the", 2 "fixture alpha", 3 "fixture mid, a", 4 "10 fixtures"
	// creator keys: 1 "fixture_b", 2 "fixture_c,  fixture_a" ('_' sorts before 'f', then the key's
	// leading '_' is stripped), 3 "Anonymous" (capital A sorts first), 4 "fixture_b"
	for _, tc := range []struct {
		name string
		f    model.WorkFilter
		want []int64
	}{
		{"title", model.WorkFilter{Sort: model.WorkSortTitle}, []int64{4, 2, 3, 1}},
		{"title desc", model.WorkFilter{Sort: model.WorkSortTitle, Direction: model.SortDesc}, []int64{1, 3, 2, 4}},
		{"creator", model.WorkFilter{Sort: model.WorkSortCreator}, []int64{3, 1, 4, 2}},
		{"creator desc", model.WorkFilter{Sort: model.WorkSortCreator, Direction: model.SortDesc},
			[]int64{2, 4, 1, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.f
			f.Limit = 10
			page, err := s.FindWorks(ctx, f)
			require.NoError(t, err)
			require.Equal(t, tc.want, workIDs(page))
		})
	}
}

// 0007 adds works.title_sort and creator_sort and backfills them (its Go step).
func TestMigrateWorkSortKeys(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	old := openAt(t, dir, 6)
	_, err := old.meta.ExecContext(ctx, `
		INSERT INTO works (id, title, anonymous, first_seen) VALUES
			(1, 'The Fixture', 0, 1), (2, 'Fixture Two', 1, 1), (3, '', 0, 1);
		INSERT INTO creators (id, user_login, pseud) VALUES
			(1, 'fixture_author', 'Fixture_Pseud'), (2, 'fixture_other', 'another_pseud');
		INSERT INTO work_creators (work_id, creator_id, position) VALUES (1, 1, 0), (1, 2, 1), (2, 1, 0);`)
	require.NoError(t, err)
	require.NoError(t, old.meta.Close())

	s, err := Open(ctx, dir, Options{})
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	type keys struct{ title, creator string }
	var got []keys
	require.NoError(t, scanRows(ctx, s.meta, `SELECT title_sort, creator_sort FROM works ORDER BY id`, nil,
		func(rows *sql.Rows) error {
			var k keys
			if err := rows.Scan(&k.title, &k.creator); err != nil {
				return err
			}
			got = append(got, k)
			return nil
		}))
	require.Equal(t, []keys{{"fixture, the", "another_pseud,  fixture_pseud"}, {"fixture two", "Anonymous"}, {"", ""}}, got)
}

// BenchmarkSortKeys times the sort keys over seedWorks with synthetic creators (one or two of 1000
// per work, every 20th work anonymous): the 0007 backfill (keys cleared, then every work computed
// and written in one transaction, rolled back), and recomputing every work and one work (an
// upsert) with the keys already current, so only the computation is timed.
//
//	go test ./internal/store -run '^$' -bench 'SortKeys' -benchtime 20x
func BenchmarkSortKeys(b *testing.B) {
	s := seedWorks(b)
	ctx := context.Background()
	_, err := s.meta.ExecContext(ctx, `
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 1000)
		INSERT INTO creators (id, user_login, pseud) SELECT i, 'fixture_user_' || i, 'Fixture Pseud ' || i FROM n;
		INSERT INTO work_creators (work_id, creator_id, position) SELECT id, 1 + id % 1000, 0 FROM works;
		INSERT OR IGNORE INTO work_creators (work_id, creator_id, position)
			SELECT id, 1 + (id * 7) % 1000, 1 FROM works WHERE id % 4 = 0;
		UPDATE works SET anonymous = 1 WHERE id % 20 = 0;`)
	require.NoError(b, err)
	b.Run("backfill", func(b *testing.B) {
		for b.Loop() {
			tx, err := s.meta.BeginTx(ctx, nil)
			require.NoError(b, err)
			_, err = tx.ExecContext(ctx, `UPDATE works SET title_sort = '', creator_sort = ''`)
			require.NoError(b, err)
			require.NoError(b, backfillSortKeys(ctx, tx))
			require.NoError(b, tx.Rollback())
		}
	})
	require.NoError(b, backfillSortKeys(ctx, s.meta))
	for _, bc := range []struct {
		name string
		sel  string
		args []any
	}{
		{"current/all", `SELECT id FROM works`, nil},
		{"current/work", `VALUES (?)`, []any{3000}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			for b.Loop() {
				require.NoError(b, updateSortKeys(ctx, s.meta, bc.sel, bc.args...))
			}
		})
	}
}
