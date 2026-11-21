package convert

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
)

func TestCreatorFilter(t *testing.T) {
	assert.Equal(t, model.CreatorFilter{}, CreatorFilter(request.CreatorQuery{}), "{} filters nothing")
	assert.Equal(t, model.CreatorFilter{Name: "fixture author", Sort: model.CreatorSortWorks, Direction: model.SortAsc},
		CreatorFilter(request.CreatorQuery{Name: "fixture author", Sort: "works", Direction: "asc"}))
}

// A valid body never makes FindCreators fail: the request's caps are the store's.
func TestCreatorQueryBoundsMatchModel(t *testing.T) {
	assert.Equal(t, model.MaxCreatorNameWords, request.MaxCreatorNameQueryWords)
}
