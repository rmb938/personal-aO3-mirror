package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Content-free change details for logs. Values are compared in memory only; what leaves this
// file is field names and counts, never titles, summaries, notes, tag or creator names.

// metaFields are the works columns a page fetch rewrites (stats are left out: they change on
// nearly every fetch). Order is the order they are reported in.
var metaFields = []string{"title", "summary_html", "language", "chapters_expected", "complete",
	"restricted", "anonymous", "notes_html", "endnotes_html", "published", "revised_at"}

// metaSets are the work's related rows, keyed so that any change to a row shows as one removed
// and one added. Order is the order they are reported in.
var metaSets = []struct{ name, query string }{
	{"tags", `SELECT tag_id FROM work_tags WHERE work_id = ?`},
	{"creators", `SELECT creator_id FROM work_creators WHERE work_id = ?`},
	{"series", `SELECT series_id || ':' || position FROM series_works WHERE work_id = ?`},
	{"collections", `SELECT collection_id FROM work_collections WHERE work_id = ?`},
	{"chapters", `SELECT position || ':' || COALESCE(chapter_id, '') || ':' || COALESCE(title, '') || ':' ||
		COALESCE(published, '') FROM chapters WHERE work_id = ?`},
}

type metaSnapshot struct {
	fields []string
	sets   map[string]map[string]bool
}

func snapshotMeta(ctx context.Context, tx *sql.Tx, workID int64) (metaSnapshot, error) {
	snap := metaSnapshot{fields: make([]string, len(metaFields)), sets: map[string]map[string]bool{}}
	cols := make([]string, len(metaFields))
	for i, f := range metaFields {
		cols[i] = "COALESCE(CAST(" + f + " AS TEXT), '')"
	}
	dst := make([]any, len(snap.fields))
	for i := range snap.fields {
		dst[i] = &snap.fields[i]
	}
	q := `SELECT ` + strings.Join(cols, ", ") + ` FROM works WHERE id = ?`
	if err := tx.QueryRowContext(ctx, q, workID).Scan(dst...); err != nil {
		return snap, err
	}
	for _, set := range metaSets {
		keys, err := queryKeys(ctx, tx, set.query, workID)
		if err != nil {
			return snap, fmt.Errorf("%s: %w", set.name, err)
		}
		snap.sets[set.name] = keys
	}
	return snap, nil
}

func queryKeys(ctx context.Context, tx *sql.Tx, q string, workID int64) (keys map[string]bool, err error) {
	rows, err := tx.QueryContext(ctx, q, workID)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	keys = map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys[k] = true
	}
	return keys, rows.Err()
}

// diffMeta lists what changed between two snapshots: field names ("title"), and for related
// rows the added and removed counts ("tags+2-1").
func diffMeta(before, after metaSnapshot) []string {
	out := []string{}
	for i, f := range metaFields {
		if before.fields[i] != after.fields[i] {
			out = append(out, f)
		}
	}
	for _, set := range metaSets {
		b, a := before.sets[set.name], after.sets[set.name]
		added, removed := 0, 0
		for k := range a {
			if !b[k] {
				added++
			}
		}
		for k := range b {
			if !a[k] {
				removed++
			}
		}
		if added+removed > 0 {
			out = append(out, fmt.Sprintf("%s+%d-%d", set.name, added, removed))
		}
	}
	return out
}
