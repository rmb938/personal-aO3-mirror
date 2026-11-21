package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// isCrossover is AO3's FandomCrossover.check_for_crossover (lib/fandom_crossover.rb) over a
// work's distinct fandoms (each already replaced by its merger: synonyms carry no meta tags on
// AO3) and their groups (the fandom and every meta tag above it). Two fandoms are related when
// their groups share a tag; a work is a crossover when no one of its fandoms is related to all
// the others. One fandom is never a crossover, nor is a work with none.
func isCrossover(groups []map[int64]bool) bool {
	if len(groups) <= 1 {
		return false
	}
	related := func(a, b map[int64]bool) bool {
		for id := range a {
			if b[id] {
				return true
			}
		}
		return false
	}
	for _, g1 := range groups {
		if !slices.ContainsFunc(groups, func(g2 map[int64]bool) bool { return !related(g1, g2) }) {
			return false // g1 is related to every fandom
		}
	}
	return true
}

// fandomGroups computes and caches fandom groups (a tag and its meta tags, transitively) for
// one recompute.
type fandomGroups struct {
	tx     querier
	groups map[int64]map[int64]bool
}

func (g *fandomGroups) group(ctx context.Context, id int64) (map[int64]bool, error) {
	if grp, ok := g.groups[id]; ok {
		return grp, nil
	}
	// A breadth-first walk up tag_meta, like AO3's boundary loop; the set ends a cycle.
	grp := map[int64]bool{id: true}
	for boundary := []int64{id}; len(boundary) > 0; {
		var next []int64
		for _, t := range boundary {
			metas, err := queryInt64s(ctx, g.tx, `SELECT meta_id FROM tag_meta WHERE sub_id = ?`, t)
			if err != nil {
				return nil, err
			}
			for _, m := range metas {
				if !grp[m] {
					grp[m] = true
					next = append(next, m)
				}
			}
		}
		boundary = next
	}
	g.groups[id] = grp
	return grp, nil
}

// workFandoms is a work's stored crossover value and its distinct fandoms (mergers applied).
type workFandoms struct {
	id        int64
	crossover bool
	fandoms   []int64
}

// loadWorkFandoms reads workFandoms for the works whose ids the subquery sel returns (args bind
// its parameters). Works with no fandom are included (with none), so a stale crossover on them
// is cleared.
func loadWorkFandoms(ctx context.Context, tx querier, sel string, args ...any) ([]workFandoms, error) {
	var out []workFandoms
	at := map[int64]int{} // work id → index in out
	if err := scanRows(ctx, tx, `SELECT id, crossover FROM works WHERE id IN (`+sel+`)`, args, func(rows *sql.Rows) error {
		var w workFandoms
		if err := rows.Scan(&w.id, &w.crossover); err != nil {
			return err
		}
		at[w.id] = len(out)
		out = append(out, w)
		return nil
	}); err != nil {
		return nil, err
	}
	// +t.type keeps the planner off tags_type: one work's few tags are read by work id, not
	// every Fandom tag probed for it.
	err := scanRows(ctx, tx, `SELECT wt.work_id, COALESCE(t.merger_id, t.id) FROM work_tags wt
		JOIN tags t ON t.id = wt.tag_id WHERE wt.work_id IN (`+sel+`) AND +t.type = 'Fandom'`, args,
		func(rows *sql.Rows) error {
			var id, fandom int64
			if err := rows.Scan(&id, &fandom); err != nil {
				return err
			}
			if w := &out[at[id]]; !slices.Contains(w.fandoms, fandom) {
				w.fandoms = append(w.fandoms, fandom)
			}
			return nil
		})
	return out, err
}

// scanRows runs q and calls fn for each row.
func scanRows(ctx context.Context, tx querier, q string, args []any, fn func(*sql.Rows) error) (err error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// updateCrossover recomputes works.crossover for the works whose ids the subquery sel returns
// (args bind its parameters), writes the rows whose value changed and returns how many did.
func updateCrossover(ctx context.Context, tx querier, sel string, args ...any) (int64, error) {
	works, err := loadWorkFandoms(ctx, tx, sel, args...)
	if err != nil {
		return 0, err
	}
	fg := &fandomGroups{tx: tx, groups: map[int64]map[int64]bool{}}
	var changed int64
	for _, w := range works {
		groups := make([]map[int64]bool, len(w.fandoms))
		if len(w.fandoms) > 1 { // one fandom or none: no groups needed
			for i, f := range w.fandoms {
				if groups[i], err = fg.group(ctx, f); err != nil {
					return changed, err
				}
			}
		}
		if c := isCrossover(groups); c != w.crossover {
			if _, err := tx.ExecContext(ctx, `UPDATE works SET crossover = ? WHERE id = ?`, boolInt(c), w.id); err != nil {
				return changed, err
			}
			changed++
		}
	}
	return changed, nil
}

// backfillCrossover is migration 0006's Go step: it computes crossover for every work.
func backfillCrossover(ctx context.Context, tx querier) error {
	_, err := updateCrossover(ctx, tx, `SELECT id FROM works`)
	return err
}

// tagLinks is what a tag's info contributes to crossover: its type, merger, meta tags and the
// synonyms merged into it.
type tagLinks struct {
	typ      model.TagType
	merger   int64
	metas    []int64
	synonyms []int64
}

func (a tagLinks) equal(b tagLinks) bool {
	return a.typ == b.typ && a.merger == b.merger && slices.Equal(a.metas, b.metas) &&
		slices.Equal(a.synonyms, b.synonyms)
}

func loadTagLinks(ctx context.Context, tx querier, id int64) (tagLinks, error) {
	var l tagLinks
	var merger *int64
	if err := tx.QueryRowContext(ctx, `SELECT type, merger_id FROM tags WHERE id = ?`, id).Scan(&l.typ, &merger); err != nil {
		return l, err
	}
	if merger != nil {
		l.merger = *merger
	}
	var err error
	if l.metas, err = queryInt64s(ctx, tx, `SELECT meta_id FROM tag_meta WHERE sub_id = ? ORDER BY meta_id`, id); err != nil {
		return l, err
	}
	l.synonyms, err = queryInt64s(ctx, tx, `SELECT id FROM tags WHERE merger_id = ? ORDER BY id`, id)
	return l, err
}

// updateTagCrossover recomputes crossover for the works a tag info update can affect, when the
// update changed a fandom's links (before and after are loadTagLinks around it). Those are the
// works whose fandom groups hold the tag, or that are tagged with a synonym of it: exactly the
// works a tag filter on it matches (tagFilterWorks). Tag info pages never change a tag's sub
// tags, so the set after the update covers the set before it.
func updateTagCrossover(ctx context.Context, tx querier, tagID int64, before, after tagLinks) (int64, error) {
	if before.equal(after) || (before.typ != model.TagFandom && after.typ != model.TagFandom) {
		return 0, nil
	}
	return updateCrossover(ctx, tx, tagFilterWorks(1), tagID)
}
