package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// WorkView maps one stored work with its relations and chapter list.
func WorkView(v controllers.WorkView) dto.Work {
	w := v.Work
	return dto.Work{
		ID:               w.ID,
		Status:           dto.WorkStatus(v.Work.Status),
		Title:            w.Title,
		Creators:         convSlice(v.Creators, workCreator),
		Anonymous:        w.Anonymous,
		Restricted:       w.Restricted,
		SummaryHTML:      w.SummaryHTML,
		NotesHTML:        w.NotesHTML,
		EndNotesHTML:     w.EndNotesHTML,
		Language:         w.Language,
		Words:            w.Words,
		ChaptersPosted:   w.ChaptersPosted,
		ChaptersExpected: w.ChaptersExpected,
		Complete:         w.Complete,
		PublishedOn:      w.Published,
		RevisedOn:        w.Revised,
		Stats:            workStats(w.Stats),
		Tags:             convSlice(v.Tags, workTag),
		Series:           convSlice(v.Series, workSeries),
		Collections:      convSlice(v.Collections, workCollection),
		Chapters:         convSlice(v.Chapters, chapterRef),
		LastFetchedAt:    utc(v.Work.LastFetched),
	}
}

// WorkSkin maps a work's skin CSS ("" for none).
func WorkSkin(css string) dto.WorkSkin {
	return dto.WorkSkin{CSS: css}
}

func workStats(s model.Stats) dto.WorkStats {
	return dto.WorkStats{Kudos: s.Kudos, Hits: s.Hits, Comments: s.Comments, Bookmarks: s.Bookmarks}
}

func workCreator(c store.CreatorRef) dto.WorkCreator {
	return dto.WorkCreator{ID: c.ID, User: c.User, Pseud: c.Pseud}
}

func workTag(t store.TagRef) dto.WorkTag {
	return dto.WorkTag{ID: t.ID, Name: t.Name, Type: dto.TagType(t.Type)}
}

func workCollection(c store.CollectionRef) dto.WorkCollection {
	return dto.WorkCollection{ID: c.ID, Name: c.Name, Title: c.Title}
}

// workSeries maps one series part; the total is null until the series page was fetched.
func workSeries(p store.WorkSeriesPart) dto.WorkSeries {
	c := p.Coverage
	return dto.WorkSeries{
		ID: p.ID, Title: p.Title, Position: p.Position, CompleteLocally: !c.Partial(), WorksTotal: worksTotal(c),
		WorksMirrored: c.Mirrored, MissingPositions: c.MissingPositions, MissingWorkIDs: c.MissingWorkIDs,
	}
}

// worksTotal is a series' AO3 work count, null until its series page was fetched.
func worksTotal(c store.SeriesCoverage) *int {
	if !c.Fetched {
		return nil
	}
	return new(c.WorksTotal)
}

func chapterRef(c model.Chapter) dto.ChapterRef {
	return dto.ChapterRef{ID: c.ID, Position: c.Position, Title: c.Title, PublishedOn: c.Published}
}
