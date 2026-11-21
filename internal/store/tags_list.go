package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// Tag listing for the API's tag search (QUERY /tags). Results hold tag names (fanwork
// content): never log them.

// TagRow is a listed tag: its wrangling state and how many stored works are tagged with it.
type TagRow struct {
	TagInfo
	Uses int // stored works (any status) tagged with this tag itself, as AO3's uses
}

// tagUses counts the stored works tagged with t itself. It reads the covering
// work_tags (tag_id, work_id) index (meta migration 0005).
const tagUses = `(SELECT COUNT(*) FROM work_tags wt WHERE wt.tag_id = t.id)`

// MaxTagPage bounds FindTags's limit: one page of a tag listing.
const MaxTagPage = 100

// FindTags returns up to f.Limit (at most MaxTagPage) tags matching f in f's order, skipping the first f.Offset.
// The page's ids are picked first (a deferred join), so rows skipped by a deep offset are never
// read in full. By name, the page walks the tags name index; by uses, every matching tag's uses
// is counted, so that cost grows with the matches and their works.
func (s *ReadTx) FindTags(ctx context.Context, f model.TagFilter) ([]TagRow, error) {
	if f.Offset < 0 || f.Limit < 1 || f.Limit > MaxTagPage {
		return nil, fmt.Errorf("store: find tags: bad page (offset %d, limit %d)", f.Offset, f.Limit)
	}
	q, args, err := tagPageQuery(f)
	if err != nil {
		return nil, err
	}
	return queryRows(ctx, s.meta, func(r *sql.Rows, t *TagRow) error {
		return scanTagInfo(r.Scan, &t.TagInfo, &t.Uses)
	}, q, append(args, f.Limit, f.Offset)...)
}

// CountTags returns the number of tags matching f's filters.
func (s *ReadTx) CountTags(ctx context.Context, f model.TagFilter) (int, error) {
	where, args, err := tagWhere(f)
	if err != nil {
		return 0, err
	}
	var n int
	err = s.meta.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags t`+where, args...).Scan(&n)
	return n, err
}

// tagPageQuery returns FindTags's query and its args, less the trailing LIMIT and OFFSET binds.
// Every order ends with the name and id, so equal uses page in a stable order; AO3 breaks a
// uses tie by name ascending, whatever the direction.
func tagPageQuery(f model.TagFilter) (string, []any, error) {
	where, args, err := tagWhere(f)
	if err != nil {
		return "", nil, err
	}
	dir, err := tagSortDir(f)
	if err != nil {
		return "", nil, err
	}
	switch f.SortOrDefault() {
	case model.TagSortName:
		order := ` ORDER BY t.name ` + dir + `, t.id ` + dir
		return `SELECT ` + tagInfoCols + `, ` + tagUses + ` FROM tags t WHERE t.id IN (
			SELECT t.id FROM tags t` + where + order + ` LIMIT ? OFFSET ?)` + order, args, nil
	case model.TagSortUses:
		page := `SELECT t.id, ` + tagUses + ` AS uses FROM tags t` + where +
			` ORDER BY uses ` + dir + `, t.name, t.id LIMIT ? OFFSET ?`
		return `SELECT ` + tagInfoCols + `, p.uses FROM tags t JOIN (` + page + `) p ON p.id = t.id
			ORDER BY p.uses ` + dir + `, t.name, t.id`, args, nil
	default:
		return "", nil, fmt.Errorf("store: tags: unknown sort %q", f.Sort)
	}
}

// tagSortDir returns f's direction as SQL.
func tagSortDir(f model.TagFilter) (string, error) {
	d := f.Direction
	if d == model.SortDefault {
		d = f.SortOrDefault().DefaultDirection()
	}
	switch d {
	case model.SortAsc:
		return "ASC", nil
	case model.SortDesc:
		return "DESC", nil
	default:
		return "", fmt.Errorf("store: tags: unknown sort direction %q", f.Direction)
	}
}

// tagWhere builds the WHERE clause (with a leading space) and its args for f's filters, over
// tags as t. Values are always parameters; only constant SQL is spliced in.
func tagWhere(f model.TagFilter) (string, []any, error) {
	var b strings.Builder
	b.WriteString(` WHERE 1=1`)
	var args []any
	cond := func(sql string, a ...any) {
		b.WriteString(` AND `)
		b.WriteString(sql)
		args = append(args, a...)
	}
	if f.Name != "" {
		words := model.TagNameWords(f.Name)
		if len(words) == 0 || len(words) > model.MaxTagNameWords {
			return "", nil, fmt.Errorf("store: tags: %d name words (1..%d)", len(words), model.MaxTagNameWords)
		}
		for _, w := range words {
			cond(`t.name LIKE ? ESCAPE '\'`, `%`+escapeLike(w)+`%`)
		}
	}
	if n := len(f.Types); n > 0 {
		if n > model.MaxTagFilterTypes {
			return "", nil, fmt.Errorf("store: tags: %d types (max %d)", n, model.MaxTagFilterTypes)
		}
		types := make([]any, n)
		for i, t := range f.Types {
			types[i] = string(t)
		}
		cond(`t.type IN (`+placeholders(n)+`)`, types...)
	}
	if f.Canonical != nil {
		cond(`t.canonical = ?`, boolInt(*f.Canonical))
	}
	if len(f.ParentIDs) > model.MaxTagFilterParents {
		return "", nil, fmt.Errorf("store: tags: %d parent ids (max %d)", len(f.ParentIDs), model.MaxTagFilterParents)
	}
	for _, id := range f.ParentIDs {
		cond(`t.id IN (SELECT r.child_id FROM tag_parents r WHERE r.parent_id = ?)`, id)
	}
	return b.String(), args, nil
}

// escapeLike escapes s for a LIKE pattern with ESCAPE '\', so its % and _ match themselves.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
