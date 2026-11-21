package handlers

import (
	"net/http"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/convert"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

// GetCollection is GET /api/v1/collections/{id}: a stored collection and its stored works count.
func (a *Handlers) GetCollection(r *http.Request) (any, error) {
	id, err := runtime.OnlyPathID(r, dto.ParamID)
	if err != nil {
		return nil, err
	}
	v, err := a.ctl.GetCollection(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return convert.CollectionView(v), nil
}

// ListCollectionWorks is GET /api/v1/collections/{id}/works (sort, direction, page, per_page):
// ListWorks for the works in the collection, as AO3's /collections/{name}/works.
func (a *Handlers) ListCollectionWorks(r *http.Request) (any, error) {
	return a.listWorks(r, a.ctl.ListCollectionWorks)
}

// QueryCollectionWorks is QUERY /api/v1/collections/{id}/works: QueryWorks within the
// collection's works.
func (a *Handlers) QueryCollectionWorks(r *http.Request) (any, error) {
	return a.queryWorks(r, a.ctl.ListCollectionWorks)
}
