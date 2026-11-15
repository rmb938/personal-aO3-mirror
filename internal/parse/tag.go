package parse

import (
	"bytes"
	"regexp"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// TagInfo is a parsed /tags/<tag> page (tags/show.html.erb).
type TagInfo struct {
	Name      string
	Type      model.TagType
	Canonical bool
	MergerOf  string   // "has been made a synonym of X": X
	Parents   []string // more general tags (fandoms for characters; media for fandoms)
	Synonyms  []string
	MetaTags  []string
}

// category NAME strings (app/models/*::NAME) to tag types.
var tagCategoryNames = map[string]model.TagType{
	"Fandom": model.TagFandom, "Character": model.TagCharacter, "Relationship": model.TagRelation,
	"Additional Tags": model.TagFreeform, "Freeform": model.TagFreeform, "Rating": model.TagRating,
	"Warning": model.TagWarning, "Archive Warning": model.TagWarning, "Category": model.TagCategory,
	"Media": model.TagMedia,
}

var belongsTo = regexp.MustCompile(`This tag belongs to the (.+?) Category`)

func ParseTag(page []byte) (TagInfo, *Report, error) {
	start := time.Now()
	r := newReport("tag")
	defer func() { r.Duration = time.Since(start) }()
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page))
	if err != nil {
		return TagInfo{}, r, err
	}
	home, err := r.need("div.tag.home", doc.Find("div.tag.home").First())
	if err != nil {
		return TagInfo{}, r, err
	}
	var t TagInfo
	h2, err := r.need("div.tag.home h2.heading", home.Find("div.primary.header h2.heading").First())
	if err != nil {
		return t, r, err
	}
	t.Name = clean(h2.Text())
	t.Type = model.TagUnknown
	home.ChildrenFiltered("p").EachWithBreak(func(_ int, p *goquery.Selection) bool {
		m := belongsTo.FindStringSubmatch(clean(p.Text()))
		if m == nil {
			return true
		}
		if typ, ok := tagCategoryNames[m[1]]; ok {
			t.Type = typ
		}
		t.Canonical = p.Find(`a[href*="canonicaldef"]`).Length() > 0
		return false
	})
	names := func(sel string) []string {
		var out []string
		r.sel(sel, home.Find(sel)).Each(func(_ int, a *goquery.Selection) {
			if tg := tagFromLink(model.TagUnknown, a); tg.Name != "" {
				out = append(out, tg.Name)
			}
		})
		return out
	}
	t.Parents = names("div.parent a.tag")
	t.Synonyms = names("div.synonym a.tag")
	t.MetaTags = names("div.meta a.tag")
	if m := names("div.merger a.tag"); len(m) > 0 {
		t.MergerOf = m[0]
	}
	r.Fields["type"] = string(t.Type) // fixed vocabulary, not content
	r.Fields["canonical"] = t.Canonical
	r.Fields["has_merger"] = t.MergerOf != ""
	r.Counts["parents"] = len(t.Parents)
	r.Counts["synonyms"] = len(t.Synonyms)
	r.Counts["meta"] = len(t.MetaTags)
	return t, r, nil
}
