package store

import (
	"context"
	"database/sql"
	"errors"
)

// Collection reads for the API's collection views. Results hold collection names and titles
// (fanwork content): never log them.

// ErrNoCollection means no collections row has the requested id.
var ErrNoCollection = errors.New("store: no such collection")

// CollectionByID returns a stored collection, or ErrNoCollection.
func (s *ReadTx) CollectionByID(ctx context.Context, id int64) (CollectionRef, error) {
	c := CollectionRef{ID: id}
	err := s.meta.QueryRowContext(ctx, `SELECT name, title FROM collections WHERE id = ?`, id).Scan(&c.Name, &c.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNoCollection
	}
	return c, err
}
