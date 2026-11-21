package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// CreatorView maps one stored creator and its works count.
func CreatorView(v controllers.CreatorView) dto.Creator {
	return creator(v.Creator, v.Works)
}

// creator maps a stored creator with works stored works, for the view and the listing.
func creator(c store.CreatorRef, works int) dto.Creator {
	return dto.Creator{ID: c.ID, User: c.User, Pseud: c.Pseud, Works: works, MirrorSourceURL: pseudSourceURL(c)}
}

// pseudSourceURL is the public AO3 works URL of a creator's pseud, which a source add accepts.
func pseudSourceURL(c store.CreatorRef) string {
	// BasePath fails only for an unknown kind or a pseud target without a slash.
	p, _ := ao3url.BasePath(ao3url.Source{Kind: ao3url.KindPseud, Target: c.User + "/" + c.Pseud})
	return httpx.DefaultBaseURL + p
}
