package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// Creator reads for the API's creator views. A creator is one AO3 pseud (creators row).
// Results hold logins and pseuds (fanwork content): never log them.

// ErrNoCreator means no creators row has the requested id.
var ErrNoCreator = errors.New("store: no such creator")

// CreatorByID returns a stored creator, or ErrNoCreator.
func (s *ReadTx) CreatorByID(ctx context.Context, id int64) (CreatorRef, error) {
	c := CreatorRef{ID: id}
	err := s.meta.QueryRowContext(ctx, `SELECT user_login, pseud FROM creators WHERE id = ?`, id).Scan(&c.User, &c.Pseud)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNoCreator
	}
	return c, err
}

// CreatorRow is a listed creator and how many stored works it is on.
type CreatorRow struct {
	CreatorRef
	Works int // stored works (any status) with this creator in the byline
}

// creatorWorks counts the stored works of creator c. It reads the covering work_creators
// (creator_id, work_id) index (meta migration 0010); a work_creators row always has a works
// row (ON DELETE CASCADE), so this equals CountWorks with the creator scope.
const creatorWorks = `(SELECT COUNT(*) FROM work_creators wc WHERE wc.creator_id = c.id)`

// MaxCreatorPage bounds FindCreators's limit: one page of a creator listing.
const MaxCreatorPage = 100

// FindCreators returns up to f.Limit (at most MaxCreatorPage) creators matching f in f's
// order, skipping the first f.Offset, each with its works count. The page's ids are picked
// first (a deferred join). By name, the page walks the creators_name index; by works, every
// matching creator's works are counted, so that cost grows with the matches and their works.
func (s *ReadTx) FindCreators(ctx context.Context, f model.CreatorFilter) ([]CreatorRow, error) {
	if f.Offset < 0 || f.Limit < 1 || f.Limit > MaxCreatorPage {
		return nil, fmt.Errorf("store: find creators: bad page (offset %d, limit %d)", f.Offset, f.Limit)
	}
	q, args, err := creatorPageQuery(f)
	if err != nil {
		return nil, err
	}
	return queryRows(ctx, s.meta, func(r *sql.Rows, c *CreatorRow) error {
		return r.Scan(&c.ID, &c.User, &c.Pseud, &c.Works)
	}, q, append(args, f.Limit, f.Offset)...)
}

// CountCreators returns the number of creators matching f's filters.
func (s *ReadTx) CountCreators(ctx context.Context, f model.CreatorFilter) (int, error) {
	where, args, err := creatorWhere(f)
	if err != nil {
		return 0, err
	}
	var n int
	err = s.meta.QueryRowContext(ctx, `SELECT COUNT(*) FROM creators c`+where, args...).Scan(&n)
	return n, err
}

// creatorNameOrder orders creators as the creators_name index, then by id, in dir.
func creatorNameOrder(dir string) string {
	return `c.pseud COLLATE NOCASE ` + dir + `, c.user_login COLLATE NOCASE ` + dir + `, c.id ` + dir
}

// creatorPageQuery returns FindCreators's query and its args, less the trailing LIMIT and
// OFFSET binds. Every order ends with the name and id, so equal works counts page in a stable
// order; a works tie breaks by name ascending, whatever the direction (as tags by uses).
func creatorPageQuery(f model.CreatorFilter) (string, []any, error) {
	where, args, err := creatorWhere(f)
	if err != nil {
		return "", nil, err
	}
	dir, err := creatorSortDir(f)
	if err != nil {
		return "", nil, err
	}
	const cols = `c.id, c.user_login, c.pseud, `
	switch f.SortOrDefault() {
	case model.CreatorSortName:
		order := ` ORDER BY ` + creatorNameOrder(dir)
		return `SELECT ` + cols + creatorWorks + ` FROM creators c WHERE c.id IN (
			SELECT c.id FROM creators c` + where + order + ` LIMIT ? OFFSET ?)` + order, args, nil
	case model.CreatorSortWorks:
		page := `SELECT c.id, ` + creatorWorks + ` AS works FROM creators c` + where +
			` ORDER BY works ` + dir + `, ` + creatorNameOrder("ASC") + ` LIMIT ? OFFSET ?`
		return `SELECT ` + cols + `p.works FROM creators c JOIN (` + page + `) p ON p.id = c.id
			ORDER BY p.works ` + dir + `, ` + creatorNameOrder("ASC"), args, nil
	default:
		return "", nil, fmt.Errorf("store: creators: unknown sort %q", f.Sort)
	}
}

// creatorSortDir returns f's direction as SQL.
func creatorSortDir(f model.CreatorFilter) (string, error) {
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
		return "", fmt.Errorf("store: creators: unknown sort direction %q", f.Direction)
	}
}

// creatorWhere builds the WHERE clause (with a leading space) and its args for f's filters,
// over creators as c. Values are always parameters; only constant SQL is spliced in.
func creatorWhere(f model.CreatorFilter) (string, []any, error) {
	if f.Name == "" {
		return "", nil, nil
	}
	words := model.NameWords(f.Name)
	if len(words) == 0 || len(words) > model.MaxCreatorNameWords {
		return "", nil, fmt.Errorf("store: creators: %d name words (1..%d)", len(words), model.MaxCreatorNameWords)
	}
	conds := make([]string, len(words))
	args := make([]any, 0, 2*len(words))
	for i, w := range words {
		p := `%` + escapeLike(w) + `%`
		conds[i] = `(c.pseud LIKE ? ESCAPE '\' OR c.user_login LIKE ? ESCAPE '\')`
		args = append(args, p, p)
	}
	return ` WHERE ` + strings.Join(conds, ` AND `), args, nil
}
