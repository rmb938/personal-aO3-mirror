package convert

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestCollectionView(t *testing.T) {
	ref := store.CollectionRef{ID: 5, Name: "fixture_collection", Title: "Fixture Collection"}
	assert.Equal(t, dto.Collection{ID: 5, Name: "fixture_collection", Title: "Fixture Collection", Works: 3,
		MirrorSourceURL: collectionSourceURL(ref)}, CollectionView(controllers.CollectionView{Collection: ref, Works: 3}))
}

func TestCollectionSourceURL(t *testing.T) {
	for _, name := range []string{"fixture_collection", "Fixture Collection?#%"} { // the second escaped in the path
		u := collectionSourceURL(store.CollectionRef{Name: name})
		assert.Contains(t, u, "/works", "the collection's works listing")
		src, err := ao3url.Parse(u)
		require.NoError(t, err, "a source add accepts the URL")
		assert.Equal(t, ao3url.Source{Kind: ao3url.KindCollection, Target: name}, src)
	}
}
