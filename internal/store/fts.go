package store

import (
	"context"
	"database/sql"
	"html"
	"regexp"
	"strings"
)

// The works_fts search index (meta migrations 0001 and 0008) holds one row per work, its rowid
// the work id: title, summary (HTML stripped), creators (each login and pseud), tags (names) and
// series (titles), as AO3's search fields (app/models/search/work_query.rb general_query).
// Every column comes from meta tables, so the index is rebuilt from them (updateFTS) whenever
// one changes: a work upsert, a series rename, or a series membership change.

var tagRe = regexp.MustCompile(`<[^>]*>`)

// plainText strips HTML for indexing.
func plainText(h string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(h, " "))), " ")
}

// ftsRow is one works_fts row.
type ftsRow struct {
	title, summary, creators, tags, series string
}

// updateFTS rebuilds the works_fts rows of the works whose ids the subquery sel returns (args
// bind its parameters) from their stored metadata, and writes the rows that changed. sel may
// return ids with no works row (series members we do not mirror); they are skipped.
func updateFTS(ctx context.Context, tx querier, sel string, args ...any) error {
	want := map[int64]*ftsRow{}
	var ids []int64
	if err := scanRows(ctx, tx, `SELECT id, title, summary_html FROM works WHERE id IN (`+sel+`) ORDER BY id`,
		args, func(rows *sql.Rows) error {
			var id int64
			var r ftsRow
			if err := rows.Scan(&id, &r.title, &r.summary); err != nil {
				return err
			}
			r.summary = plainText(r.summary)
			want[id] = &r
			ids = append(ids, id)
			return nil
		}); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	// Each list is joined in its stored order, so an unchanged work rebuilds an identical row.
	for _, l := range []struct {
		q   string
		sep string
		col func(*ftsRow) *string
	}{
		{`SELECT wc.work_id, c.user_login || ' ' || c.pseud FROM work_creators wc
			JOIN creators c ON c.id = wc.creator_id WHERE wc.work_id IN (` + sel + `)
			ORDER BY wc.work_id, wc.position, c.id`, " ", func(r *ftsRow) *string { return &r.creators }},
		{`SELECT wt.work_id, t.name FROM work_tags wt JOIN tags t ON t.id = wt.tag_id
			WHERE wt.work_id IN (` + sel + `) ORDER BY wt.work_id, wt.position, t.id`,
			" • ", func(r *ftsRow) *string { return &r.tags }},
		{`SELECT sw.work_id, se.title FROM series_works sw JOIN series se ON se.id = sw.series_id
			WHERE sw.work_id IN (` + sel + `) AND se.title != '' ORDER BY sw.work_id, sw.series_id`,
			" • ", func(r *ftsRow) *string { return &r.series }},
	} {
		if err := scanRows(ctx, tx, l.q, args, func(rows *sql.Rows) error {
			var id int64
			var s string
			if err := rows.Scan(&id, &s); err != nil {
				return err
			}
			if r, ok := want[id]; ok {
				if p := l.col(r); *p == "" {
					*p = s
				} else {
					*p += l.sep + s
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	have := map[int64]ftsRow{}
	if err := scanRows(ctx, tx, `SELECT rowid, title, summary, creators, tags, series FROM works_fts
		WHERE rowid IN (`+sel+`)`, args, func(rows *sql.Rows) error {
		var id int64
		var r ftsRow
		if err := rows.Scan(&id, &r.title, &r.summary, &r.creators, &r.tags, &r.series); err != nil {
			return err
		}
		have[id] = r
		return nil
	}); err != nil {
		return err
	}
	for _, id := range ids {
		w := *want[id]
		h, ok := have[id]
		if ok && h == w {
			continue
		}
		if ok {
			if _, err := tx.ExecContext(ctx, `DELETE FROM works_fts WHERE rowid = ?`, id); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO works_fts (rowid, title, summary, creators, tags, series)
			VALUES (?,?,?,?,?,?)`, id, w.title, w.summary, w.creators, w.tags, w.series); err != nil {
			return err
		}
	}
	return nil
}

// seriesWorks is a subquery for the ids of the works in the series the subquery sel returns.
func seriesWorks(sel string) string {
	return `SELECT work_id FROM series_works WHERE series_id IN (` + sel + `)`
}

// backfillFTS is migration 0008's Go step: it indexes every work.
func backfillFTS(ctx context.Context, tx querier) error {
	return updateFTS(ctx, tx, `SELECT id FROM works`)
}
