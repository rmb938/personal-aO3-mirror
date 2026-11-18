package httpx

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Path segments and query values that are safe to log verbatim. Anything else (tag names, user
// names, collection names, search text) is replaced by a short hash so logs stay content-free
// but still let you correlate requests for the same tag/user.
var safeSegments = map[string]bool{
	"works": true, "chapters": true, "series": true, "tags": true, "users": true, "pseuds": true,
	"collections": true, "bookmarks": true, "navigate": true, "feed.atom": true, "robots.txt": true,
	"media": true, "fandoms": true, "login": true, "search": true, "profile": true,
}

var safeQueryValues = map[string]bool{
	"true": true, "false": true, "asc": true, "desc": true, "t": true, "f": true,
	"revised_at": true, "created_at": true, "kudos_count": true, "hits": true, "word_count": true,
	"comments_count": true, "bookmarks_count": true, "title_to_sort_on": true,
	"authors_to_sort_on": true, "_score": true,
}

var numeric = regexp.MustCompile(`^[0-9]+$`)
var isoDate = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// Hash returns a short stable hash for log correlation of a name without revealing it.
func Hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "h" + hex.EncodeToString(sum[:4])
}

// SafePath renders path+query for logs with every non-structural value hashed.
func SafePath(u *url.URL) string {
	if u == nil {
		return ""
	}
	segs := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	for i, s := range segs {
		dec, err := url.PathUnescape(s)
		if err != nil {
			dec = s
		}
		if s == "" || numeric.MatchString(dec) || safeSegments[dec] {
			continue
		}
		segs[i] = Hash(dec)
	}
	out := "/" + strings.Join(segs, "/")
	q := u.Query()
	if len(q) == 0 {
		return out
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		for _, v := range q[k] {
			if !(numeric.MatchString(v) || isoDate.MatchString(v) || safeQueryValues[v] || v == "") {
				v = Hash(v)
			}
			sk := k
			if !isSafeKey(k) {
				sk = Hash(k)
			}
			parts = append(parts, sk+"="+v)
		}
	}
	return out + "?" + strings.Join(parts, "&")
}

var safeKey = regexp.MustCompile(`^[a-z_]+(\[[a-z_]+\])?(\[\])?$`)

func isSafeKey(k string) bool { return safeKey.MatchString(k) }
