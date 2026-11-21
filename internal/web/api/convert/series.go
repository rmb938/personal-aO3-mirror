package convert

import (
	"strconv"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// SeriesView maps one stored series with its creators and mirror coverage.
func SeriesView(v controllers.SeriesView) dto.Series {
	s, c := v.Series, v.Coverage
	return dto.Series{
		ID:               s.ID,
		Title:            s.Title,
		Creators:         convSlice(v.Creators, workCreator),
		SummaryHTML:      s.SummaryHTML,
		NotesHTML:        s.NotesHTML,
		BegunOn:          s.Begun,
		UpdatedOn:        s.Updated,
		Words:            s.Words,
		Bookmarks:        s.Bookmarks,
		Complete:         s.Complete,
		CompleteLocally:  !c.Partial(),
		WorksTotal:       worksTotal(c),
		WorksMirrored:    c.Mirrored,
		MissingPositions: c.MissingPositions,
		MissingWorkIDs:   c.MissingWorkIDs,
		WorksStored:      v.Works,
		MirrorSourceURL:  seriesSourceURL(s.ID),
		LastFetchedAt:    utc(s.LastFetched),
	}
}

// seriesSourceURL is the public AO3 URL of a series, which a source add accepts.
func seriesSourceURL(id int64) string {
	// BasePath fails only for an unknown kind.
	p, _ := ao3url.BasePath(ao3url.Source{Kind: ao3url.KindSeries, Target: strconv.FormatInt(id, 10)})
	return httpx.DefaultBaseURL + p
}
