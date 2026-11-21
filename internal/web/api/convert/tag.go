package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// TagView maps one stored tag with its merger, related tags and work count.
func TagView(v controllers.TagView) dto.Tag {
	t := v.Tag
	return dto.Tag{
		ID:            t.ID,
		Name:          t.Name,
		Type:          dto.TagType(t.Type),
		Canonical:     canonical(t),
		Merger:        convPtr(v.Merger, tagRef),
		Parents:       convSlice(v.Parents, tagGroup),
		Children:      convSlice(v.Children, tagGroup),
		MetaTags:      convSlice(v.MetaTags, tagGroup),
		SubTags:       convSlice(v.SubTags, tagGroup),
		Synonyms:      convSlice(v.Synonyms, tagGroup),
		WorksCount:    v.Works,
		InfoFetchedAt: utc(t.InfoFetchedAt),
	}
}

// canonical is t's canonical flag, nil until it is recorded.
func canonical(t store.TagInfo) *bool {
	if !t.CanonicalKnown {
		return nil
	}
	return new(t.Canonical)
}

func tagRef(t store.TagRef) dto.TagRef {
	return dto.TagRef{ID: t.ID, Name: t.Name, Type: dto.TagType(t.Type)}
}

func tagGroup(g store.TagGroup) dto.TagGroup {
	return dto.TagGroup{Type: dto.TagType(g.Type), Total: g.Total, Tags: convSlice(g.Tags, tagRef)}
}
