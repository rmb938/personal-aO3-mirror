package convert

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestCreatorList(t *testing.T) {
	a := store.CreatorRef{ID: 3, User: "fixture_author", Pseud: "fixture_pseud"}
	b := store.CreatorRef{ID: 4, User: "fixture_other", Pseud: "fixture_other"}
	got := CreatorList([]store.CreatorRow{{CreatorRef: a, Works: 7}, {CreatorRef: b}}, 12, 2, 2)
	assert.Equal(t, dto.List[dto.Creator]{Items: []dto.Creator{
		{ID: 3, User: "fixture_author", Pseud: "fixture_pseud", Works: 7, MirrorSourceURL: pseudSourceURL(a)},
		{ID: 4, User: "fixture_other", Pseud: "fixture_other", MirrorSourceURL: pseudSourceURL(b)},
	}, Page: 2, PerPage: 2, Total: 12}, got)
}

func TestCreatorListItem(t *testing.T) {
	ref := store.CreatorRef{ID: 5, User: "fixture_author", Pseud: "fixture_pseud"}
	assert.Equal(t, creator(ref, 2), creatorListItem(store.CreatorRow{CreatorRef: ref, Works: 2}),
		"the same fields as the creator view")
}
