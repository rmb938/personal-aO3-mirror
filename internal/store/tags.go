package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// TagRef is a tag id and name (names are content: never log them).
type TagRef struct {
	ID   int64
	Name string
	Type string
}

// TagsNeedingInfo returns tags of the given types never fetched or fetched before `before`,
// most-used first. Only tags used by at least one mirrored work qualify: synonyms and meta tags
// learned from tag pages get a type too, and fetching each of those would multiply requests.
func (s *Store) TagsNeedingInfo(ctx context.Context, types []string, before time.Time, limit int) ([]TagRef, error) {
	if len(types) == 0 {
		return nil, nil
	}
	args := []any{}
	for _, t := range types {
		args = append(args, t)
	}
	args = append(args, before.Unix(), limit)
	rows, err := s.Meta.QueryContext(ctx, `SELECT t.id, t.name, t.type FROM tags t
		WHERE t.type IN (`+placeholders(len(types))+`) AND t.info_fetched_at < ?
		  AND EXISTS (SELECT 1 FROM work_tags wt WHERE wt.tag_id = t.id)
		ORDER BY (SELECT COUNT(*) FROM work_tags wt WHERE wt.tag_id = t.id) DESC, t.id
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TagRef
	for rows.Next() {
		var r TagRef
		if err := rows.Scan(&r.ID, &r.Name, &r.Type); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) TagByID(ctx context.Context, id int64) (TagRef, error) {
	var r TagRef
	err := s.Meta.QueryRowContext(ctx, `SELECT id, name, type FROM tags WHERE id = ?`, id).Scan(&r.ID, &r.Name, &r.Type)
	if errors.Is(err, sql.ErrNoRows) {
		return r, errors.New("store: no such tag")
	}
	return r, err
}

// TagInfoUpdate is what a tag page says about a tag.
type TagInfoUpdate struct {
	Type      model.TagType // TagUnknown keeps the stored type
	Canonical bool
	MergerOf  string
	Parents   []string
	MetaTags  []string
	Synonyms  []string // linked as mergers of this tag
	Missing   bool     // tag page 404: only info_fetched_at is updated
}

// UpdateTagInfo records wrangling info for a tag (canonical, synonym-of, parents, metatags).
//
// AO3 only links synonyms, mergers and meta tags within one tag type, so those referenced tags
// get this tag's type (an already-known type is never overwritten). Parent tags can be of other
// types (a relationship's parents mix fandoms and characters), so they are created as Unknown
// and get a type when they appear on a work.
func (s *Store) UpdateTagInfo(ctx context.Context, tagID int64, u TagInfoUpdate) error {
	tx, err := s.Meta.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.unix()
	if u.Missing {
		if _, err := tx.ExecContext(ctx, `UPDATE tags SET info_fetched_at = ? WHERE id = ?`, now, tagID); err != nil {
			return err
		}
		return tx.Commit()
	}
	sameType := u.Type
	if sameType == model.TagUnknown || sameType == "" {
		if err := tx.QueryRowContext(ctx, `SELECT type FROM tags WHERE id = ?`, tagID).Scan(&sameType); err != nil {
			return err
		}
	}
	ref := func(name string) (int64, error) {
		return s.tagID(ctx, tx, model.Tag{Type: model.TagUnknown, Name: name})
	}
	refSame := func(name string) (int64, error) {
		return s.tagID(ctx, tx, model.Tag{Type: sameType, Name: name})
	}
	var merger any
	if u.MergerOf != "" {
		id, err := refSame(u.MergerOf)
		if err != nil {
			return err
		}
		merger = id
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tags SET canonical = ?, merger_id = ?, info_fetched_at = ?,
		type = CASE WHEN ? != 'Unknown' THEN ? ELSE type END WHERE id = ?`,
		boolInt(u.Canonical), merger, now, string(u.Type), string(u.Type), tagID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tag_parents WHERE child_id = ?`, tagID); err != nil {
		return err
	}
	for _, p := range u.Parents {
		id, err := ref(p)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO tag_parents (child_id, parent_id) VALUES (?,?)`, tagID, id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tag_meta WHERE sub_id = ?`, tagID); err != nil {
		return err
	}
	for _, m := range u.MetaTags {
		id, err := refSame(m)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO tag_meta (sub_id, meta_id) VALUES (?,?)`, tagID, id); err != nil {
			return err
		}
	}
	for _, syn := range u.Synonyms {
		id, err := refSame(syn)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tags SET merger_id = ?, canonical = COALESCE(canonical, 0) WHERE id = ? AND id != ?`,
			tagID, id, tagID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.log.Debug("tag info stored", "tag_id", tagID, "type", string(u.Type), "canonical", u.Canonical,
		"has_merger", u.MergerOf != "", "parents", len(u.Parents), "meta", len(u.MetaTags), "synonyms", len(u.Synonyms))
	return nil
}
