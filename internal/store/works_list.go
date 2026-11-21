package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// workSortCols maps each sort to its works column. Each column has an index (meta migrations
// 0001 and 0004); an index holds the rowid, so ORDER BY <col>, id walks it with no sort step.
var workSortCols = map[model.WorkSort]string{
	model.WorkSortRevised:   "revised_at",
	model.WorkSortPublished: "published",
	model.WorkSortWords:     "words",
	model.WorkSortHits:      "hits",
	model.WorkSortKudos:     "kudos",
	model.WorkSortComments:  "comments",
	model.WorkSortBookmarks: "bookmarks",
}

// workOrder returns f's ORDER BY clause (with a leading space). Every order ends with id in the
// same direction, so equal sort values page in a stable order.
func workOrder(f model.WorkFilter) (string, error) {
	sort := f.Sort
	if sort == "" {
		sort = model.WorkSortRevised
	}
	col, ok := workSortCols[sort]
	if !ok {
		return "", fmt.Errorf("store: works: unknown sort %q", sort)
	}
	dir := "DESC"
	switch f.Direction {
	case model.SortDefault, model.SortDesc:
	case model.SortAsc:
		dir = "ASC"
	default:
		return "", fmt.Errorf("store: works: unknown sort direction %q", f.Direction)
	}
	return ` ORDER BY ` + col + ` ` + dir + `, id ` + dir, nil
}

// workWhere builds the WHERE clause (with a leading space) and its args for f's filters. Values
// are always parameters; only column names from this file are spliced in.
func workWhere(f model.WorkFilter) (string, []any, error) {
	var b strings.Builder
	b.WriteString(` WHERE 1=1`)
	var args []any
	cond := func(sql string, a ...any) {
		b.WriteString(` AND ` + sql)
		args = append(args, a...)
	}
	for _, ids := range [][]int64{f.Tags, f.ExcludeTags} {
		if len(ids) > model.MaxWorkFilterTags {
			return "", nil, fmt.Errorf("store: works: %d tag ids (max %d)", len(ids), model.MaxWorkFilterTags)
		}
	}
	for _, id := range f.Tags {
		cond(`id IN (`+tagFilterWorks(1)+`)`, id)
	}
	if n := len(f.ExcludeTags); n > 0 {
		ids := make([]any, n)
		for i, id := range f.ExcludeTags {
			ids[i] = id
		}
		cond(`id NOT IN (`+tagFilterWorks(n)+`)`, ids...)
	}
	if f.Complete != nil {
		cond(`complete = ?`, boolInt(*f.Complete))
	}
	if f.Crossover != nil {
		cond(`crossover = ?`, boolInt(*f.Crossover))
	}
	if f.SingleChapter {
		cond(`chapters_expected = 1`)
	}
	if f.Language != "" {
		cond(`language = ?`, f.Language)
	}
	if n := len(f.Statuses); n > 0 {
		if n > model.MaxWorkStatuses {
			return "", nil, fmt.Errorf("store: works: %d statuses (max %d)", n, model.MaxWorkStatuses)
		}
		for _, st := range f.Statuses {
			if !slices.Contains(model.WorkStatuses(), st) {
				return "", nil, fmt.Errorf("store: works: unknown status %q", st)
			}
			args = append(args, string(st))
		}
		// Unary + keeps the planner off works_status: most works are ok, so that index selects
		// little and would force sorting every match instead of walking the sort index.
		b.WriteString(` AND +status IN (?` + strings.Repeat(`,?`, n-1) + `)`)
	}
	if f.Fetched != nil {
		if *f.Fetched {
			cond(`last_fetched > 0`)
		} else {
			cond(`last_fetched = 0`)
		}
	}
	for _, r := range []struct {
		col string
		r   model.IntRange
	}{
		{"words", f.Words}, {"kudos", f.Kudos}, {"hits", f.Hits}, {"comments", f.Comments},
		{"bookmarks", f.Bookmarks},
	} {
		if r.r.Min != nil {
			cond(r.col+` >= ?`, *r.r.Min)
		}
		if r.r.Max != nil {
			cond(r.col+` <= ?`, *r.r.Max)
		}
	}
	for _, r := range []struct {
		col string
		r   model.DateRange
	}{{"revised_at", f.Revised}, {"published", f.Published}} {
		if r.r.From.IsZero() && r.r.To.IsZero() {
			continue
		}
		cond(r.col + ` != ''`) // unknown dates sort before every real one
		if !r.r.From.IsZero() {
			cond(r.col+` >= ?`, r.r.From.Format(time.DateOnly))
		}
		if !r.r.To.IsZero() {
			cond(r.col+` <= ?`, r.r.To.Format(time.DateOnly))
		}
	}
	return b.String(), args, nil
}

// tagFilterWorks returns a subquery for the ids of works matching any of n tag ids (n bound
// parameters), like AO3's filter_ids: works tagged with one of the tags, any sub tag below it
// (tag_meta, transitively; UNION stops on a cycle), or a synonym (merger) of either. AO3 indexes
// a work under its tags, their filters (the merger, or the tag itself when canonical) and every
// meta tag above those (app/models/concerns/filterable.rb), which is the same set read from the
// other side. A synonym id matches only works tagged with it, as on AO3.
func tagFilterWorks(n int) string {
	return `SELECT wt.work_id FROM work_tags wt WHERE wt.tag_id IN (
		WITH RECURSIVE sub(id) AS (VALUES (?)` + strings.Repeat(`, (?)`, n-1) + `
			UNION SELECT m.sub_id FROM tag_meta m JOIN sub ON m.meta_id = sub.id)
		SELECT id FROM sub UNION SELECT t.id FROM tags t JOIN sub ON t.merger_id = sub.id)`
}

// FindWorks returns up to f.Limit works matching f in f's order, skipping the first f.Offset.
// The page's ids are picked first over the sort index (a deferred join), so rows skipped by a
// deep offset are never read in full. It holds fanwork content: never log it.
func (s *ReadTx) FindWorks(ctx context.Context, f model.WorkFilter) ([]WorkRow, error) {
	if f.Offset < 0 || f.Limit < 1 {
		return nil, fmt.Errorf("store: find works: bad page (offset %d, limit %d)", f.Offset, f.Limit)
	}
	where, args, err := workWhere(f)
	if err != nil {
		return nil, err
	}
	order, err := workOrder(f)
	if err != nil {
		return nil, err
	}
	q := `SELECT ` + workRowCols + ` FROM works WHERE id IN (SELECT id FROM works` + where + order +
		` LIMIT ? OFFSET ?)` + order
	rows, err := s.meta.QueryContext(ctx, q, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkRow
	for rows.Next() {
		r, err := scanWorkRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountWorks returns the number of works matching f's filters.
func (s *ReadTx) CountWorks(ctx context.Context, f model.WorkFilter) (int, error) {
	where, args, err := workWhere(f)
	if err != nil {
		return 0, err
	}
	var n int
	err = s.meta.QueryRowContext(ctx, `SELECT COUNT(*) FROM works`+where, args...).Scan(&n)
	return n, err
}
