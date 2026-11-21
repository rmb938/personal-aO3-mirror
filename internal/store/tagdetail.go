package store

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// Readers for one tag's relations, for the API's tag view. Results hold tag names (fanwork
// content): never log them.

// TagRelation names a set of tags related to one tag, as recorded from tag pages.
type TagRelation int

const (
	TagParents  TagRelation = iota // tags this tag lists as parents (tag_parents, child side)
	TagChildren                    // tags listing this tag as a parent (tag_parents, parent side)
	TagMetaTags                    // this tag's direct meta tags (tag_meta, sub side)
	TagSubTags                     // tags with this tag as a direct meta tag (tag_meta, meta side)
	TagSynonyms                    // tags merged into this tag (tags.merger_id)
)

// tagRelationFrom is each relation's FROM clause: the related tags as t, filtered by the one
// bound tag id. Each reads an index on the given id's side.
var tagRelationFrom = map[TagRelation]string{
	TagParents:  `tag_parents r JOIN tags t ON t.id = r.parent_id WHERE r.child_id = ?`,
	TagChildren: `tag_parents r JOIN tags t ON t.id = r.child_id WHERE r.parent_id = ?`,
	TagMetaTags: `tag_meta r JOIN tags t ON t.id = r.meta_id WHERE r.sub_id = ?`,
	TagSubTags:  `tag_meta r JOIN tags t ON t.id = r.sub_id WHERE r.meta_id = ?`,
	TagSynonyms: `tags t WHERE t.merger_id = ?`,
}

// MaxTagGroup bounds the tags RelatedTags returns per type.
const MaxTagGroup = 100

// TagGroup is the related tags of one type: the first ones by name, and how many there are.
type TagGroup struct {
	Type  model.TagType
	Total int      // every related tag of this type
	Tags  []TagRef // at most the limit, by name (case-insensitive), then id
}

// RelatedTags returns a tag's related tags of one relation, grouped by type in
// model.TagTypes() order, each group cut to limit (1..MaxTagGroup) tags. AO3's tag page groups
// them the same way. An unknown tag has none.
func (s *ReadTx) RelatedTags(ctx context.Context, tagID int64, rel TagRelation, limit int) ([]TagGroup, error) {
	q, err := relatedTagsQuery(rel)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > MaxTagGroup {
		return nil, fmt.Errorf("store: related tags: limit %d (1..%d)", limit, MaxTagGroup)
	}
	type row struct {
		tag   TagRef
		total int
	}
	rows, err := queryRows(ctx, s.meta, func(r *sql.Rows, x *row) error {
		return r.Scan(&x.tag.ID, &x.tag.Name, &x.tag.Type, &x.total)
	}, q, tagID, limit)
	if err != nil {
		return nil, err
	}
	var out []TagGroup
	for _, r := range rows {
		if n := len(out); n == 0 || out[n-1].Type != r.tag.Type {
			out = append(out, TagGroup{Type: r.tag.Type, Total: r.total})
		}
		out[len(out)-1].Tags = append(out[len(out)-1].Tags, r.tag)
	}
	order := model.TagTypes()
	slices.SortStableFunc(out, func(a, b TagGroup) int {
		return cmp.Compare(typeRank(order, a.Type), typeRank(order, b.Type))
	})
	return out, nil
}

// relatedTagsQuery is RelatedTags's query for rel, binding the tag id and the per-type limit.
// The window numbers each type's tags by name and counts them, so a group's total costs no
// second query. Only the constant FROM clause is concatenated.
func relatedTagsQuery(rel TagRelation) (string, error) {
	from, ok := tagRelationFrom[rel]
	if !ok {
		return "", fmt.Errorf("store: related tags: unknown relation %d", int(rel))
	}
	return `SELECT id, name, type, n FROM (
			SELECT t.id, t.name, t.type,
				ROW_NUMBER() OVER (PARTITION BY t.type ORDER BY t.name, t.id) AS rn,
				COUNT(*) OVER (PARTITION BY t.type) AS n
			FROM ` + from + `)
		WHERE rn <= ? ORDER BY type, rn`, nil
}

// typeRank is t's index in order; a type not in it (none today) sorts last.
func typeRank(order []model.TagType, t model.TagType) int {
	if i := slices.Index(order, t); i >= 0 {
		return i
	}
	return len(order)
}
