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

func TestCreatorView(t *testing.T) {
	ref := store.CreatorRef{ID: 5, User: "fixture_author", Pseud: "fixture_pseud"}
	assert.Equal(t, dto.Creator{ID: 5, User: "fixture_author", Pseud: "fixture_pseud", Works: 3,
		MirrorSourceURL: pseudSourceURL(ref)}, CreatorView(controllers.CreatorView{Creator: ref, Works: 3}))
}

func TestCreator(t *testing.T) {
	ref := store.CreatorRef{ID: 6, User: "fixture_author", Pseud: "fixture_pseud"}
	assert.Equal(t, dto.Creator{ID: 6, User: "fixture_author", Pseud: "fixture_pseud",
		MirrorSourceURL: pseudSourceURL(ref)}, creator(ref, 0), "a creator on no stored work")
}

func TestPseudSourceURL(t *testing.T) {
	for _, c := range []store.CreatorRef{
		{User: "fixture_author", Pseud: "fixture_pseud"},
		{User: "fixture_author", Pseud: "Fixture Pseud?#%"}, // escaped in the path
	} {
		u := pseudSourceURL(c)
		assert.Contains(t, u, "/works", "the pseud's works listing")
		src, err := ao3url.Parse(u)
		require.NoError(t, err, "a source add accepts the URL")
		assert.Equal(t, ao3url.Source{Kind: ao3url.KindPseud, Target: c.User + "/" + c.Pseud}, src)
	}
}
