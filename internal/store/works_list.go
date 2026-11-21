package store

import (
	"context"
	"errors"
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
	model.WorkSortTitle:     "title_sort",   // store/sortkeys.go, meta migration 0007
	model.WorkSortCreator:   "creator_sort", // store/sortkeys.go, meta migration 0007
}

// workOrder returns f's ORDER BY clause (with a leading space). Every order ends with id in the
// same direction, so equal sort values page in a stable order.
func workOrder(f model.WorkFilter) (string, error) {
	sort := f.SortOrDefault()
	col, ok := workSortCols[sort]
	if !ok {
		return "", fmt.Errorf("store: works: unknown sort %q", sort)
	}
	dir, err := sortDir(f)
	if err != nil {
		return "", err
	}
	return ` ORDER BY ` + col + ` ` + dir + `, id ` + dir, nil
}

// sortDir returns f's direction as SQL.
func sortDir(f model.WorkFilter) (string, error) {
	d := f.Direction
	if d == model.SortDefault {
		d = f.SortOrDefault().DefaultDirection()
	}
	switch d {
	case model.SortDesc:
		return "DESC", nil
	case model.SortAsc:
		return "ASC", nil
	default:
		return "", fmt.Errorf("store: works: unknown sort direction %q", f.Direction)
	}
}

// ftsMatch turns search text into an FTS5 MATCH expression. The text is never FTS5 syntax:
// each term (model.SearchTerms) is a quoted string, which FTS5 tokenizes into a word or phrase,
// so every operator is built here. Included terms are ANDed, and excluded ones removed with NOT.
func ftsMatch(q string) (string, error) {
	terms := model.SearchTerms(q)
	if len(terms) > model.MaxSearchTerms {
		return "", fmt.Errorf("store: works: %d search terms (max %d)", len(terms), model.MaxSearchTerms)
	}
	var incl, excl []string
	for _, t := range terms {
		s := `"` + strings.ReplaceAll(t.Text, `"`, `""`) + `"`
		if t.Exclude {
			excl = append(excl, s)
		} else {
			incl = append(incl, s)
		}
	}
	if len(incl) == 0 {
		return "", errors.New("store: works: search has no term to match")
	}
	m := strings.Join(incl, " AND ")
	if len(excl) > 0 {
		m = `(` + m + `) NOT (` + strings.Join(excl, " OR ") + `)`
	}
	return m, nil
}

// ftsRanked selects each work matching the MATCH parameter as fid with its score (higher is a
// better match). bm25 weighs the works_fts columns (title, summary, creators, tags) with AO3's
// field boosts (title^7, creators^5, the rest 1; app/models/search/work_query.rb
// general_query). bm25 is lower for better matches, so the score is its negation.
const ftsRanked = `SELECT rowid AS fid, -bm25(works_fts, 7.0, 1.0, 5.0, 1.0) AS score
	FROM works_fts WHERE works_fts MATCH ?`

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
	if f.Query != "" {
		m, err := ftsMatch(f.Query)
		if err != nil {
			return "", nil, err
		}
		cond(`id IN (SELECT rowid FROM works_fts WHERE works_fts MATCH ?)`, m)
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
	q, args, err := workPageQuery(f)
	if err != nil {
		return nil, err
	}
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

// workPageQuery returns FindWorks's query and its args, less the trailing LIMIT and OFFSET.
func workPageQuery(f model.WorkFilter) (string, []any, error) {
	if f.SortOrDefault() == model.WorkSortBestMatch {
		return bestMatchPageQuery(f)
	}
	where, args, err := workWhere(f)
	if err != nil {
		return "", nil, err
	}
	order, err := workOrder(f)
	if err != nil {
		return "", nil, err
	}
	return `SELECT ` + workRowCols + ` FROM works WHERE id IN (SELECT id FROM works` + where + order +
		` LIMIT ? OFFSET ?)` + order, args, nil
}

// bestMatchPageQuery ranks every work matching f.Query (ftsRanked), keeps those matching f's
// other filters and pages them by score, then id. There is no index on a score, so the cost
// grows with the number of matches.
func bestMatchPageQuery(f model.WorkFilter) (string, []any, error) {
	if f.Query == "" {
		return "", nil, errors.New("store: works: best_match sort needs a search query")
	}
	m, err := ftsMatch(f.Query)
	if err != nil {
		return "", nil, err
	}
	rest := f
	rest.Query = "" // matched by the join with ftsRanked instead
	where, args, err := workWhere(rest)
	if err != nil {
		return "", nil, err
	}
	dir, err := sortDir(f)
	if err != nil {
		return "", nil, err
	}
	page := `SELECT m.fid, m.score FROM (` + ftsRanked + `) m JOIN works ON works.id = m.fid` + where +
		` ORDER BY m.score ` + dir + `, m.fid ` + dir + ` LIMIT ? OFFSET ?`
	q := `SELECT ` + workRowCols + ` FROM works JOIN (` + page + `) p ON p.fid = works.id
		ORDER BY p.score ` + dir + `, p.fid ` + dir
	return q, append([]any{m}, args...), nil
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
