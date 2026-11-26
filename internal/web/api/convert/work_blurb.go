package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// WorkBlurb maps one work of a listing with its blurb relations.
func WorkBlurb(b controllers.WorkBlurb) dto.WorkBlurb {
	w := b.Work
	return dto.WorkBlurb{
		ID:               w.ID,
		Status:           dto.WorkStatus(w.Status),
		Title:            w.Title,
		Creators:         convSlice(b.Creators, workCreator),
		Anonymous:        w.Anonymous,
		Restricted:       w.Restricted,
		SummaryHTML:      w.SummaryHTML,
		Language:         w.Language,
		Words:            w.Words,
		ChaptersPosted:   w.ChaptersPosted,
		ChaptersExpected: w.ChaptersExpected,
		Complete:         w.Complete,
		PublishedOn:      w.Published,
		RevisedOn:        w.Revised,
		Stats:            workStats(w.Stats),
		Tags:             convSlice(b.Tags, workTag),
		Series:           convSlice(b.Series, workSeriesPart),
		LastFetchedAt:    utc(w.LastFetched),
	}
}

// WorkBlurbList maps one page of a work listing.
func WorkBlurbList(items []controllers.WorkBlurb, total, page, perPage int) dto.List[dto.WorkBlurb] {
	return list(items, WorkBlurb, total, page, perPage)
}

func workSeriesPart(p store.SeriesPart) dto.WorkSeriesPart {
	return dto.WorkSeriesPart{ID: p.ID, Title: p.Title, Position: p.Position}
}
