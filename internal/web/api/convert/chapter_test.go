package convert

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestChapter(t *testing.T) {
	c := model.Chapter{ID: 11, Position: 2, Title: "Fixture Chapter", Published: "2026-01-02",
		SummaryHTML: "<p>fixture summary</p>", BodyHTML: "<p>fixture body</p>", NotesHTML: "<p>fixture notes</p>",
		EndNotesHTML: "<p>fixture end</p>"}
	assert.Equal(t, dto.Chapter{ID: 11, Position: 2, Title: "Fixture Chapter", PublishedOn: "2026-01-02",
		SummaryHTML: "<p>fixture summary</p>", NotesHTML: "<p>fixture notes</p>", EndNotesHTML: "<p>fixture end</p>",
		BodyHTML: "<p>fixture body</p>"}, Chapter(c))
	assert.Equal(t, dto.Chapter{Position: 1}, Chapter(model.Chapter{Position: 1}), "a chapter with no stored content")
}

func TestWorkChapters(t *testing.T) {
	got := WorkChapters([]model.Chapter{{Position: 1, BodyHTML: "<p>fixture one</p>"}, {Position: 2, BodyHTML: "<p>fixture two</p>"}})
	assert.Equal(t, dto.WorkChapters{Chapters: []dto.Chapter{{Position: 1, BodyHTML: "<p>fixture one</p>"},
		{Position: 2, BodyHTML: "<p>fixture two</p>"}}}, got)
	assert.Equal(t, dto.WorkChapters{}, WorkChapters(nil), "none: nil, written as []")
}
