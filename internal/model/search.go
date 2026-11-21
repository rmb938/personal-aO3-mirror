package model

import (
	"strings"
	"unicode"
)

// MaxSearchTerms bounds the terms of a WorkFilter.Query (SearchTerms).
const MaxSearchTerms = 16

// SearchTerm is one word or quoted phrase of a search query.
type SearchTerm struct {
	Text    string // the word, or the phrase's words joined by single spaces
	Exclude bool   // a leading '-': no match may contain it (AO3's NOT)
}

// SearchTerms splits a search query into terms, a subset of AO3's query syntax: words, "quoted
// phrases" (an unclosed quote runs to the end) and a leading '-' to exclude a word or phrase.
// Every other term must match (AO3's default_operator AND). Nothing else is syntax: AND, OR,
// field:value, wildcards and the like are plain words.
//
// A term with no letter or digit is dropped, since the search index has nothing to match it with.
func SearchTerms(q string) []SearchTerm {
	var out []SearchTerm
	add := func(text string, exclude bool) {
		text = strings.Join(strings.Fields(text), " ")
		if strings.IndexFunc(text, isTokenRune) >= 0 {
			out = append(out, SearchTerm{Text: text, Exclude: exclude})
		}
	}
	for q != "" {
		q = strings.TrimLeftFunc(q, unicode.IsSpace)
		exclude := strings.HasPrefix(q, "-")
		if exclude {
			q = q[1:]
		}
		if rest, ok := strings.CutPrefix(q, `"`); ok {
			phrase, after, _ := strings.Cut(rest, `"`)
			add(phrase, exclude)
			q = after
			continue
		}
		// A word ends at a space or at a quote, which starts a phrase.
		end := strings.IndexFunc(q, func(r rune) bool { return unicode.IsSpace(r) || r == '"' })
		if end < 0 {
			end = len(q)
		}
		add(q[:end], exclude)
		q = q[end:]
	}
	return out
}

// isTokenRune reports whether r is part of a word in the search index: letters, digits and
// private-use characters, as for SQLite FTS5's unicode61 tokenizer.
func isTokenRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Co, r)
}
