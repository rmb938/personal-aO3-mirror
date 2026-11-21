package store

import (
	"context"
	"database/sql"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Title and creator sort keys, ported from AO3's app/models/work.rb (Work#sorted_title and
// Work#authors_to_sort_on, compared byte-wise as Elasticsearch keywords, which SQLite's BINARY
// collation matches). Ruby's downcase is full Unicode case mapping and Go's ToLower is simple
// mapping; they differ only for a few special casings (e.g. U+0130), which is accepted.

var (
	titleLeadingPunct = regexp.MustCompile(`^["'./]`)
	titleArticleA     = regexp.MustCompile(`^(an?) (.*)`)
	titleArticleThe   = regexp.MustCompile(`^the (.*)`)
	titleLeadingDigit = regexp.MustCompile(`^[0-9]`)
	creatorPunct      = regexp.MustCompile(`^[+\-=_?!'"./]`) // AO3 SORTED_AUTHOR_REGEX
)

// titleSortKey is AO3's sorted_title: lowercased, one leading quote, dot or slash removed, a
// leading "a"/"an" then "the" moved to the end ("the x" → "x, the"), and a key starting with a
// digit left-padded with zeros to 5 characters (the whole key, as Ruby's rjust(5, "0")).
func titleSortKey(title string) string {
	k := strings.ToLower(title)
	k = titleLeadingPunct.ReplaceAllString(k, "")
	k = titleArticleA.ReplaceAllString(k, "$2, $1")
	k = titleArticleThe.ReplaceAllString(k, "$1, the")
	if titleLeadingDigit.MatchString(k) {
		if n := utf8.RuneCountInString(k); n < 5 {
			k = strings.Repeat("0", 5-n) + k
		}
	}
	return k
}

// sortCreator is one stored creator: AO3 pseud name and user login.
type sortCreator struct{ pseud, login string }

// creatorSortKey is AO3's authors_to_sort_on: "Anonymous" for an anonymous work, else the pseud
// names ordered like Pseud#<=> (name, then login, both lowercased), joined by ",  ", lowercased,
// with one leading punctuation character removed.
func creatorSortKey(anonymous bool, cs []sortCreator) string {
	if anonymous {
		return "Anonymous"
	}
	cs = slices.Clone(cs)
	slices.SortFunc(cs, func(a, b sortCreator) int {
		if c := strings.Compare(strings.ToLower(a.pseud), strings.ToLower(b.pseud)); c != 0 {
			return c
		}
		return strings.Compare(strings.ToLower(a.login), strings.ToLower(b.login))
	})
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.pseud
	}
	return creatorPunct.ReplaceAllString(strings.ToLower(strings.Join(names, ",  ")), "")
}

// updateSortKeys recomputes works.title_sort and creator_sort for the works whose ids the
// subquery sel returns (args bind its parameters) from their stored title, anonymity and
// creators, and writes the rows whose keys changed.
func updateSortKeys(ctx context.Context, tx querier, sel string, args ...any) error {
	type work struct {
		id                    int64
		title, titleKey, cKey string
		anonymous             bool
		creators              []sortCreator
	}
	var works []work
	at := map[int64]int{}
	if err := scanRows(ctx, tx, `SELECT id, title, anonymous, title_sort, creator_sort FROM works
		WHERE id IN (`+sel+`) ORDER BY id`, args, func(rows *sql.Rows) error {
		var w work
		if err := rows.Scan(&w.id, &w.title, &w.anonymous, &w.titleKey, &w.cKey); err != nil {
			return err
		}
		at[w.id] = len(works)
		works = append(works, w)
		return nil
	}); err != nil {
		return err
	}
	if err := scanRows(ctx, tx, `SELECT wc.work_id, c.pseud, c.user_login FROM work_creators wc
		JOIN creators c ON c.id = wc.creator_id WHERE wc.work_id IN (`+sel+`)`, args, func(rows *sql.Rows) error {
		var id int64
		var c sortCreator
		if err := rows.Scan(&id, &c.pseud, &c.login); err != nil {
			return err
		}
		if i, ok := at[id]; ok {
			works[i].creators = append(works[i].creators, c)
		}
		return nil
	}); err != nil {
		return err
	}
	for _, w := range works {
		tk, ck := titleSortKey(w.title), creatorSortKey(w.anonymous, w.creators)
		if tk == w.titleKey && ck == w.cKey {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE works SET title_sort = ?, creator_sort = ? WHERE id = ?`,
			tk, ck, w.id); err != nil {
			return err
		}
	}
	return nil
}

// backfillSortKeys is migration 0007's Go step: it computes the sort keys for every work.
func backfillSortKeys(ctx context.Context, tx querier) error {
	return updateSortKeys(ctx, tx, `SELECT id FROM works`)
}
