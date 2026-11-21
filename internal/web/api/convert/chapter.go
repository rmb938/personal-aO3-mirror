package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// Chapter maps one chapter with its content.
func Chapter(c model.Chapter) dto.Chapter {
	return dto.Chapter{
		ID: c.ID, Position: c.Position, Title: c.Title, PublishedOn: c.Published,
		SummaryHTML: c.SummaryHTML, NotesHTML: c.NotesHTML, EndNotesHTML: c.EndNotesHTML, BodyHTML: c.BodyHTML,
	}
}

// WorkChapters maps every chapter of a work with its content, in order.
func WorkChapters(cs []model.Chapter) dto.WorkChapters {
	return dto.WorkChapters{Chapters: convSlice(cs, Chapter)}
}
