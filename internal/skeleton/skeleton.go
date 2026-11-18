// Package skeleton reduces an HTML page to its structure so it can be shared with Claude for parser
// debugging without exposing any fanwork content (see CLAUDE.md).
//
// Replacement is consistent within one run and keyed by a random per-run secret (never stored),
// so equal inputs map to equal placeholders on the same page (tag link text matches its href,
// work_123 matches /works/123, "3/3" stays equal) but placeholders cannot be reversed by guessing
// common names, and nothing links two runs.
//
// Rules:
//   - AO3 UI vocabulary (labels like "Words:", canonical rating/warning/category names) is kept
//   - other words become lorem-ipsum words (case shape kept; length not preserved)
//   - numbers >= 100 keep digit count and comma grouping with random values; smaller ones kept
//   - dates stay valid dates in the same format; relative dates ("3 days ago") keep their wording
//   - .userstuff contents (work text, summaries, notes) collapse to one lorem placeholder
//   - <script>, <style>, <noscript>, <template> contents are dropped
//   - attributes: class, id, role, rel, lang, dir, type, name kept (numbers/words masked in id,
//     name and class); href/src/action masked by path shape; title kept only for UI vocabulary;
//     everything else dropped
package skeleton

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"
)

// Skeletonizer holds the per-run key. Use New for real runs; NewWithKey for deterministic tests.
type Skeletonizer struct{ key []byte }

func New() *Skeletonizer {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		panic(err)
	}
	return &Skeletonizer{key: k}
}

func NewWithKey(key []byte) *Skeletonizer { return &Skeletonizer{key: key} }

// Skeletonize uses a fresh random key.
func Skeletonize(r io.Reader) ([]byte, error) { return New().Skeletonize(r) }

// Header is prepended to every skeleton. It deliberately names no source URL (that would reveal
// which work/tag/user the page came from).
const Header = `<!-- AUTO-GENERATED SKELETON by cmd/skeletonize. Do not edit by hand.
     Structure-only copy of an AO3 page: text, names and values replaced with consistent
     placeholders (lorem ipsum, random numbers/dates); no fanwork content. Safe for Claude to read.
     To regenerate, see README.md "Parser skeletons". -->
`

func (s *Skeletonizer) Skeletonize(r io.Reader) ([]byte, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	s.walk(doc)
	var buf bytes.Buffer
	buf.WriteString(Header)
	if err := html.Render(&buf, doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Skeletonizer) h(kind, v string) uint64 {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(kind + "\x00" + v))
	return binary.BigEndian.Uint64(m.Sum(nil))
}

// ---- vocabulary ----

// uiText is exact AO3 interface text (from otwarchive templates/locales) and canonical tags.
var uiText = map[string]bool{}

func init() {
	for _, s := range []string{
		// labels
		"Fandoms:", "Fandom:", "Tags", "Summary", "Summary:", "Series", "Series:", "Language:", "Words:",
		"Chapters:", "Collections:", "Comments:", "Kudos:", "Bookmarks:", "Hits:", "Rating:", "Ratings:",
		"Archive Warning:", "Archive Warnings:", "Category:", "Categories:", "Relationship:", "Relationships:",
		"Character:", "Characters:", "Additional Tags:", "Stats:", "Published:", "Updated:", "Completed:",
		"Work Header", "Notes:", "Chapter Text", "Work Text:", "Creator:", "Creators:", "Series Begun:",
		"Series Updated:", "Description:", "Complete:", "Yes", "No", "Works:", "Works", "Anonymous", "by",
		"for", "Part", "of", "Chapter by Chapter", "Entire Work", "Pages Navigation", "Listing Series",
		"Series Metadata", "Mystery Work", "Restricted", "Hidden by Administrator", "Navigation and Actions",
		"Error 404", "Chapter Index", "Comments", "Kudos", "Bookmarks", "Share", "Download", "Subscribe",
		"Previous", "Next", "← Previous", "Next →", "Previous Work", "Next Work", "← Previous Work",
		"Next Work →", "← Previous Chapter", "Next Chapter →", "Symbols key", "Work in Progress",
		"Complete Work", "Series in Progress", "Complete Series", "…", "Bookmarked by", "Main Content",
		"Skip header", "Original Creator:", "Original Creators:", "Translation:", "Inspired by:",
		// ratings
		"General Audiences", "Teen And Up Audiences", "Mature", "Explicit", "Not Rated",
		// warnings
		"Creator Chose Not To Use Archive Warnings", "Graphic Depictions Of Violence", "Major Character Death",
		"No Archive Warnings Apply", "Rape/Non-Con", "Underage Sex", "Underage",
		// categories
		"F/F", "F/M", "Gen", "M/M", "Multi", "Other", "No category",
	} {
		uiText[s] = true
	}
}

func isUI(t string) bool {
	if uiText[t] {
		return true
	}
	// joined title strings: "F/M, Gen"
	if strings.Contains(t, ", ") {
		for p := range strings.SplitSeq(t, ", ") {
			if !uiText[p] {
				return false
			}
		}
		return true
	}
	return false
}

// Patterned UI text: kept with numbers randomized.
var uiPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^[0-9][0-9,]* - [0-9][0-9,]* of [0-9][0-9,]* (Works?|Bookmarks?|Series)( in| by)?$`),
	regexp.MustCompile(`^[0-9][0-9,]* (Works?|Bookmarks?|Series)( in| by)?$`),
	regexp.MustCompile(`^Chapter [0-9]+$`),
	regexp.MustCompile(`^(Chapter|Part) [0-9]+ of$`),
	regexp.MustCompile(`^[0-9][0-9,]*/([0-9][0-9,]*|\?)$`),
	// AO3's time_ago_in_words (app/helpers/translation_helper.rb), not the Rails one.
	regexp.MustCompile(`^(less than )?[0-9]+ (minute|hour|day)s? ago$`),
}

var lorem = strings.Fields(`lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor
incididunt ut labore et dolore magna aliqua enim ad minim veniam quis nostrud exercitation ullamco laboris
nisi aliquip ex ea commodo consequat duis aute irure in reprehenderit voluptate velit esse cillum fugiat nulla
pariatur excepteur sint occaecat cupidatat non proident sunt culpa qui officia deserunt mollit anim id est
laborum curabitur pretium tincidunt lacus nunc pulvinar sapien ligula gravida quam viverra metus vestibulum
porta ante fusce suscipit libero mauris rhoncus aenean vel elementum augue vitae sagittis pellentesque
habitant morbi tristique senectus netus malesuada fames turpis egestas proin nibh nisl condimentum mattis
feugiat varius placerat orci vulputate odio facilisis leo integer quisque congue risus tellus faucibus
blandit mi bibendum arcu cursus eget dui accumsan purus semper posuere lectus urna nec ultrices hendrerit
dictum fermentum iaculis eu volutpat lobortis mollis ornare massa sollicitudin aliquam eros donec pharetra
convallis maecenas ultricies cras nam neque scelerisque sodales imperdiet vivamus dapibus rutrum`)

// ---- text transforms ----

var (
	isoDate  = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	rfcDate  = regexp.MustCompile(`^[0-9]{1,2} [A-Z][a-z]{2} [0-9]{4}$`)
	numRun   = regexp.MustCompile(`[0-9][0-9,]*[0-9]|[0-9]`)
	dateBase = time.Date(2008, 9, 1, 0, 0, 0, 0, time.UTC) // AO3 open beta
)

// Text maps one text node.
func (s *Skeletonizer) Text(t string) string {
	trim := strings.TrimSpace(t)
	if trim == "" {
		return t
	}
	lead := t[:len(t)-len(strings.TrimLeft(t, " \t\r\n"))]
	tail := t[len(strings.TrimRight(t, " \t\r\n")):]
	return lead + s.textCore(trim) + tail
}

// uiPrefixes are AO3 interface sentences that start a text node followed by other text.
var uiPrefixes = []*regexp.Regexp{
	regexp.MustCompile(`^This tag belongs to the (Fandom|Character|Relationship|Additional Tags|Rating|Archive Warning|Category|Media|Unsorted Tag) Category\.`),
}

func (s *Skeletonizer) textCore(t string) string {
	if isUI(t) {
		return t
	}
	for _, p := range uiPrefixes {
		if m := p.FindString(t); m != "" {
			return m + s.words(t[len(m):])
		}
	}
	if isoDate.MatchString(t) {
		return s.date(t, "2006-01-02")
	}
	if rfcDate.MatchString(t) {
		return s.date(t, "02 Jan 2006")
	}
	for _, p := range uiPatterns {
		if p.MatchString(t) {
			return numRun.ReplaceAllStringFunc(t, s.number)
		}
	}
	return s.words(t)
}

func (s *Skeletonizer) date(v, layout string) string {
	days := int(s.h("date", v) % uint64(time.Since(dateBase).Hours()/24+1)) //nolint:gosec // G115: modulo days since dateBase, far below MaxInt
	return dateBase.AddDate(0, 0, days).Format(layout)
}

// number keeps digit count and comma grouping; equal inputs give equal outputs.
// Mapped values never start with 0.
func (s *Skeletonizer) number(v string) string {
	digits := strings.ReplaceAll(v, ",", "")
	// Numbers under 100 (chapter counts/positions, series parts) are kept: they carry almost no
	// identifying information and keep "3/3" and chapter-count checks meaningful.
	if n, err := strconv.Atoi(digits); err == nil && n < 100 {
		return v
	}
	out := make([]byte, len(digits))
	for i := range out {
		d := byte(s.h("num", digits+":"+strconv.Itoa(i)) % 10)
		if i == 0 {
			d = 1 + byte(s.h("num", digits+":first")%9)
		}
		out[i] = '0' + d
	}
	// re-insert commas at the same positions (counted from the right)
	parts := strings.Split(v, ",")
	res := make([]string, len(parts))
	j := len(out)
	for i := len(parts) - 1; i >= 0; i-- {
		n := len(parts[i])
		res[i] = string(out[j-n : j])
		j -= n
	}
	return strings.Join(res, ",")
}

// words replaces letter runs with lorem words (case shape kept), digit runs with numbers, and
// keeps ASCII punctuation/spaces. Other characters become "-".
func (s *Skeletonizer) words(t string) string {
	var b strings.Builder
	rs := []rune(t)
	for i := 0; i < len(rs); {
		r := rs[i]
		switch {
		case unicode.IsLetter(r):
			j := i
			for j < len(rs) && (unicode.IsLetter(rs[j]) || rs[j] == '\'') {
				j++
			}
			b.WriteString(s.word(string(rs[i:j])))
			i = j
		case unicode.IsDigit(r):
			j := i
			for j < len(rs) && (unicode.IsDigit(rs[j]) || (rs[j] == ',' && j+1 < len(rs) && unicode.IsDigit(rs[j+1]))) {
				j++
			}
			b.WriteString(s.number(string(rs[i:j])))
			i = j
		case r < 128 && (unicode.IsPunct(r) || unicode.IsSpace(r) || unicode.IsSymbol(r)):
			b.WriteRune(r)
			i++
		default:
			b.WriteRune('-')
			i++
		}
	}
	return b.String()
}

func (s *Skeletonizer) word(w string) string {
	l := lorem[s.h("word", strings.ToLower(w))%uint64(len(lorem))]
	rs := []rune(w)
	switch {
	case len(rs) > 1 && strings.ToUpper(w) == w:
		return strings.ToUpper(l)
	case unicode.IsUpper(rs[0]):
		return strings.ToUpper(l[:1]) + l[1:]
	}
	return l
}

// slug maps a name used in a URL segment (tag/user/pseud/collection) consistently with Text.
func (s *Skeletonizer) slug(seg string) string {
	dec, err := url.PathUnescape(seg)
	if err != nil {
		dec = seg
	}
	fromParam := strings.NewReplacer("*s*", "/", "*a*", "&", "*d*", ".", "*q*", "?", "*h*", "#")
	toParam := strings.NewReplacer("/", "*s*", "&", "*a*", ".", "*d*", "?", "*q*", "#", "*h*")
	name := fromParam.Replace(dec)
	return strings.ReplaceAll(url.PathEscape(toParam.Replace(s.textCore(name))), "%2A", "*") // AO3 keeps *s* literal
}

// ---- tree walk ----

func hasClass(n *html.Node, cls string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" && slices.Contains(strings.Fields(a.Val), cls) {
			return true
		}
	}
	return false
}

func (s *Skeletonizer) walk(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch c.Type {
		case html.TextNode:
			c.Data = s.Text(c.Data)
		case html.CommentNode:
			c.Data = " " + s.words(strings.TrimSpace(c.Data)) + " "
		case html.ElementNode:
			c.Attr = s.filterAttrs(c.Attr)
			switch strings.ToLower(c.Data) {
			case "script", "style", "noscript", "template":
				clearChildren(c)
			default:
				if hasClass(c, "userstuff") {
					clearChildren(c)
					c.AppendChild(&html.Node{Type: html.TextNode, Data: "Lorem ipsum dolor sit amet."})
				} else {
					s.walk(c)
				}
			}
		case html.DoctypeNode:
		default:
			s.walk(c)
		}
		c = next
	}
}

func clearChildren(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		n.RemoveChild(c)
		c = next
	}
}

var keepAttrs = map[string]bool{"class": true, "id": true, "role": true, "rel": true, "lang": true, "dir": true, "type": true, "name": true}

var urlAttrs = map[string]bool{"href": true, "src": true, "action": true}

func (s *Skeletonizer) filterAttrs(attrs []html.Attribute) []html.Attribute {
	out := attrs[:0]
	for _, a := range attrs {
		k := strings.ToLower(a.Key)
		switch {
		case k == "class":
			a.Val = s.maskTokens(a.Val, " ")
			out = append(out, a)
		case k == "id" || k == "name":
			a.Val = s.maskID(a.Val)
			out = append(out, a)
		case keepAttrs[k]:
			out = append(out, a)
		case urlAttrs[k]:
			a.Val = s.Pattern(a.Val)
			out = append(out, a)
		case k == "title" && isUI(strings.TrimSpace(a.Val)):
			out = append(out, a)
		}
	}
	return out
}

// maskTokens masks numbers inside space-separated class tokens ("work-1001" -> "work-4821").
func (s *Skeletonizer) maskTokens(v, sep string) string {
	parts := strings.Split(v, sep)
	for i, p := range parts {
		parts[i] = numRun.ReplaceAllStringFunc(p, s.number)
	}
	return strings.Join(parts, sep)
}

var idSep = regexp.MustCompile(`[_\-]`)
var idWord = regexp.MustCompile(`^[a-z]{1,24}$`)

// maskID keeps id structure ("work_4821", "chapter-7"); numbers are mapped, lowercase structural
// words kept, anything else becomes a lorem word.
func (s *Skeletonizer) maskID(v string) string {
	parts := idSep.Split(v, -1)
	seps := idSep.FindAllString(v, -1)
	var b strings.Builder
	for i, p := range parts {
		switch {
		case p == "":
		case numRun.MatchString(p) && numRun.FindString(p) == p:
			p = s.number(p)
		case idWord.MatchString(p):
		default:
			p = strings.ToLower(s.words(p))
		}
		b.WriteString(p)
		if i < len(seps) {
			b.WriteString(seps[i])
		}
	}
	return b.String()
}

var safeWords = map[string]bool{
	"works": true, "chapters": true, "series": true, "tags": true, "users": true, "pseuds": true,
	"collections": true, "bookmarks": true, "navigate": true, "feed.atom": true, "media": true,
	"fandoms": true, "login": true, "search": true, "profile": true, "comments": true, "kudos": true,
	"downloads": true, "help": true, "admin_posts": true, "menu": true, "gifts": true, "share": true,
	"people": true, "external_works": true, "first_login_help": true, "invite_requests": true,
	"symbols-key.html": true, "faq": true, "glossary": true, "images": true, "lockblue.png": true, "lockred.png": true,
}

// segments following these are user-chosen names and are slugged consistently with page text
var nameAfter = map[string]bool{"tags": true, "users": true, "pseuds": true, "collections": true}

var safeValues = map[string]bool{"true": true, "false": true, "asc": true, "desc": true}

var (
	numeric = regexp.MustCompile(`^[0-9]+$`)
	safeKey = regexp.MustCompile(`^[a-z_]+(\[[a-z_]+\])?(\[\])?$`)
)

// Pattern masks a URL: AO3 structure kept, ids mapped, names slugged, external hosts hidden.
func (s *Skeletonizer) Pattern(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "x"
	}
	if u.Scheme == "javascript" || u.Scheme == "data" || u.Scheme == "mailto" {
		return u.Scheme + ":x"
	}
	var b strings.Builder
	external := false
	if u.Host != "" {
		b.WriteString("//")
		if strings.HasSuffix(u.Host, "archiveofourown.org") || u.Host == "ao3.org" {
			b.WriteString("ao3")
		} else {
			b.WriteString("external")
			external = true
		}
	}
	if external {
		b.WriteString("/x")
		return b.String()
	}
	if p := u.EscapedPath(); p != "" {
		segs := strings.Split(p, "/")
		for i, seg := range segs {
			switch {
			case seg == "":
			case numeric.MatchString(seg):
				segs[i] = s.number(seg)
			case i > 0 && nameAfter[segs[i-1]] && !numeric.MatchString(seg):
				segs[i] = s.slug(seg)
			case safeWords[seg]:
			default:
				segs[i] = s.slug(seg)
			}
		}
		b.WriteString(strings.Join(segs, "/"))
	}
	if q := u.Query(); len(q) > 0 {
		keys := make([]string, 0, len(q))
		for k := range q {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			sk := k
			if !safeKey.MatchString(k) {
				sk = "x"
			}
			for _, v := range q[k] {
				switch {
				case numeric.MatchString(v):
					v = s.number(v)
				case safeValues[v]:
				default:
					v = url.QueryEscape(s.words(v))
				}
				parts = append(parts, sk+"="+v)
			}
		}
		b.WriteString("?" + strings.Join(parts, "&"))
	}
	if u.Fragment != "" {
		b.WriteString("#" + s.maskID(u.Fragment))
	}
	return b.String()
}

// SaveDebug writes a skeleton of page to <dataDir>/debug/<kind>-<id>-<ts>.skel.html and returns
// the path relative to dataDir. Used when a page fails to parse, so the structure can be
// inspected safely.
func SaveDebug(dataDir, kind, id string, page []byte, now time.Time) (string, error) {
	skel, err := Skeletonize(bytes.NewReader(page))
	if err != nil {
		return "", err
	}
	dir := filepath.Join(dataDir, "debug")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%s-%s.skel.html", kind, id, now.UTC().Format("20060102T150405"))
	if err := os.WriteFile(filepath.Join(dir, name), skel, 0o600); err != nil {
		return "", err
	}
	return filepath.Join("debug", name), nil
}
