package parse

import (
	"bytes"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// metaTagClasses maps dd classes in dl.work.meta (tag_type_css_class) to tag types, in display order.
var metaTagClasses = []struct {
	cls string
	typ model.TagType
}{
	{"rating", model.TagRating},
	{"warning", model.TagWarning},
	{"category", model.TagCategory},
	{"fandom", model.TagFandom},
	{"relationship", model.TagRelation},
	{"character", model.TagCharacter},
	{"freeform", model.TagFreeform},
}

// ParseWork parses /works/<id>?view_full_work=true&view_adult=true.
func ParseWork(workID int64, page []byte) (model.Work, *Report, error) {
	start := time.Now()
	r := newReport("work")
	defer func() { r.Duration = time.Since(start) }()
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page))
	if err != nil {
		return model.Work{}, r, err
	}
	w := model.Work{ID: workID}

	meta, err := r.need("dl.work.meta", doc.Find("dl.work.meta").First())
	if err != nil {
		return w, r, err
	}
	skin, err := r.need("#workskin", doc.Find("#workskin").First())
	if err != nil {
		return w, r, err
	}
	preface := r.sel("#workskin>.preface", skin.ChildrenFiltered("div.preface").First())
	title, err := r.need("#workskin .preface h2.title", preface.Find("h2.title").First())
	if err != nil {
		return w, r, err
	}

	// Header / preface.
	w.Title = clean(title.Text())
	w.Restricted = title.Find(`img[title="Restricted"], img[alt="(Restricted)"]`).Length() > 0
	byline := r.sel(".preface h3.byline", preface.Find("h3.byline").First())
	w.Creators = creators(byline)
	w.Anonymous = len(w.Creators) == 0 && strings.Contains(byline.Text(), "Anonymous")
	w.SummaryHTML = innerHTML(r.sel(".preface .summary blockquote.userstuff", preface.Find("div.summary blockquote.userstuff")))
	w.NotesHTML = innerWithout(r.sel(".preface .notes.module", preface.ChildrenFiltered("div.notes")), "h3.heading")

	// Meta tags.
	for _, g := range metaTagClasses {
		meta.Find("dd." + g.cls + ".tags a.tag").Each(func(_ int, a *goquery.Selection) {
			w.Tags = append(w.Tags, tagFromLink(g.typ, a))
		})
		r.Counts["tags_"+g.cls] = meta.Find("dd." + g.cls + ".tags a.tag").Length()
	}
	w.Language = clean(meta.Find("dd.language").First().Text())
	meta.Find("dd.series span.series").Each(func(_ int, s *goquery.Selection) {
		pos := s.Find("span.position").First()
		a := pos.Find(`a[href*="/series/"]`).First()
		href, _ := a.Attr("href")
		if id := pathID(seriesPath, href); id > 0 {
			w.Series = append(w.Series, model.SeriesPart{ID: id, Title: clean(a.Text()), Position: atoi(pos.Text())})
		}
	})
	meta.Find("dd.collections a").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		if m := collNamePath.FindStringSubmatch(href); m != nil {
			w.Collections = append(w.Collections, model.Collection{Name: m[1], Title: clean(a.Text())})
		}
	})

	// Stats.
	stats, err := r.need("dl.work.meta dl.stats", meta.Find("dl.stats").First())
	if err != nil {
		return w, r, err
	}
	w.Published, _ = parseISODate(stats.Find("dd.published").Text())
	if d, ok := parseISODate(stats.Find("dd.status").Text()); ok {
		w.Revised = d // "Updated:" or "Completed:" (chaptered works only)
	} else {
		w.Revised, w.RevisedIsPublished = w.Published, true
	}
	statusLabel := clean(stats.Find("dt.status").Text())
	w.Words = atoi(stats.Find("dd.words").Text())
	w.ChaptersPosted, w.ChaptersExpected = chaptersPair(stats.Find("dd.chapters").Text())
	w.Complete = (w.ChaptersExpected > 0 && w.ChaptersPosted >= w.ChaptersExpected) || strings.HasPrefix(statusLabel, "Completed")
	w.Stats = parseStats(stats)

	// Work skin: <style> rendered by skins/_skin_style_block inside #work-skin, before #workskin.
	doc.Find("#work-skin > style").Each(func(_ int, s *goquery.Selection) { w.SkinCSS += s.Text() })

	// Chapters.
	chapters, err := r.need("#chapters", skin.Find("#chapters").First())
	if err != nil {
		return w, r, err
	}
	chDivs := r.sel("#chapters>div.chapter", chapters.ChildrenFiltered(`div.chapter[id^="chapter-"]`))
	if chDivs.Length() > 0 {
		chDivs.Each(func(i int, div *goquery.Selection) {
			w.Chapters = append(w.Chapters, parseChapter(i+1, div, r))
		})
	} else {
		body := r.sel("#chapters>div.userstuff", chapters.ChildrenFiltered("div.userstuff").First())
		if body.Length() == 0 {
			r.Missing = append(r.Missing, "#chapters>div.userstuff")
			return w, r, unexpected("#chapters div.userstuff")
		}
		w.Chapters = []model.Chapter{{Position: 1, BodyHTML: innerHTML(body)}}
	}

	// Afterword: work end notes.
	w.EndNotesHTML = innerHTML(r.sel("#work_endnotes blockquote.userstuff", skin.Find("#work_endnotes blockquote.userstuff")))

	// Report (content-free).
	r.textLen("title", w.Title)
	r.textLen("summary", w.SummaryHTML)
	r.textLen("notes", w.NotesHTML)
	r.textLen("endnotes", w.EndNotesHTML)
	r.textLen("skin", w.SkinCSS)
	r.num("words", w.Words)
	r.num("chapters_posted", w.ChaptersPosted)
	r.num("chapters_expected", w.ChaptersExpected)
	r.num("kudos", w.Stats.Kudos)
	r.num("hits", w.Stats.Hits)
	r.Fields["has_published"] = w.Published != ""
	r.Fields["restricted"] = w.Restricted
	r.Fields["anonymous"] = w.Anonymous
	r.Counts["creators"] = len(w.Creators)
	r.Counts["tags"] = len(w.Tags)
	r.Counts["series"] = len(w.Series)
	r.Counts["collections"] = len(w.Collections)
	r.Counts["chapters"] = len(w.Chapters)
	r.Counts["images"] = skin.Find("#chapters img").Length()
	bodyBytes := 0
	for _, c := range w.Chapters {
		bodyBytes += len(c.BodyHTML)
	}
	r.num("body_bytes", bodyBytes)
	if len(w.Chapters) != w.ChaptersPosted && w.ChaptersPosted > 0 {
		r.Fields["chapter_count_mismatch"] = true
	}
	return w, r, nil
}

func parseChapter(pos int, div *goquery.Selection, r *Report) model.Chapter {
	c := model.Chapter{Position: pos}
	pre := div.ChildrenFiltered("div.chapter.preface").First()
	h3 := pre.Find("h3.title").First()
	if a := h3.Find("a").First(); a.Length() > 0 {
		href, _ := a.Attr("href")
		c.ID = pathID(chapterPath, href)
		// Title is the text after "Chapter N: ".
		full := clean(h3.Text())
		head := clean(a.Text())
		c.Title = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(full, head), ":"))
	}
	if c.ID == 0 {
		r.Counts["chapter_missing_id"]++
	}
	c.SummaryHTML = innerHTML(pre.Find("div.summary blockquote.userstuff"))
	c.NotesHTML = innerHTML(pre.Find("div.notes blockquote.userstuff"))
	body := div.ChildrenFiltered("div.userstuff").First()
	if body.Length() == 0 {
		r.Counts["chapter_missing_body"]++
	}
	c.BodyHTML = innerWithout(body, "h3.landmark")
	c.EndNotesHTML = innerHTML(div.Find("div.end.notes blockquote.userstuff"))
	return c
}
