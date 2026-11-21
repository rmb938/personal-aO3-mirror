package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// CollectionView maps one stored collection and its works count.
func CollectionView(v controllers.CollectionView) dto.Collection {
	c := v.Collection
	return dto.Collection{ID: c.ID, Name: c.Name, Title: c.Title, Works: v.Works,
		MirrorSourceURL: collectionSourceURL(c)}
}

// collectionSourceURL is the public AO3 works URL of a collection, which a source add accepts.
func collectionSourceURL(c store.CollectionRef) string {
	// BasePath fails only for an unknown kind (or a pseud target without a slash).
	p, _ := ao3url.BasePath(ao3url.Source{Kind: ao3url.KindCollection, Target: c.Name})
	return httpx.DefaultBaseURL + p
}
