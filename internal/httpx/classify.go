package httpx

import (
	"bytes"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Class is the outcome category of one AO3 response.
type Class string

const (
	ClassOK          Class = "ok"
	ClassNotFound    Class = "not_found"    // 404: deleted or never existed
	ClassRestricted  Class = "restricted"   // redirect to login with restricted=true (archive-locked)
	ClassHidden      Class = "hidden"       // access denied: hidden by admin, unrevealed, draft
	ClassAdultGate   Class = "adult_gate"   // adult interstitial despite the view_adult cookie
	ClassRateLimited Class = "rate_limited" // 429
	ClassUnavailable Class = "unavailable"  // 5xx, Cloudflare 52x/challenge/waiting room, maintenance
	ClassUnexpected  Class = "parse_unexpected"
)

// Classes lists every Class.
func Classes() []Class {
	return []Class{ClassOK, ClassNotFound, ClassRestricted, ClassHidden, ClassAdultGate, ClassRateLimited,
		ClassUnavailable, ClassUnexpected}
}

// Kind is what the caller expected to fetch. It selects which markers must be present.
type Kind string

const (
	KindRobots  Kind = "robots"
	KindListing Kind = "listing"
	KindWork    Kind = "work"
	KindTag     Kind = "tag"
	KindSeries  Kind = "series"
	KindPage    Kind = "page" // any other AO3 HTML page
)

// Kinds lists every Kind.
func Kinds() []Kind {
	return []Kind{KindRobots, KindListing, KindWork, KindTag, KindSeries, KindPage}
}

// Cloudflare/edge markers in response bodies. Matching is case-insensitive.
var cfMarkers = []string{
	"cf_chl_", "challenge-platform", "cf-error-details", "cf-wrapper", "cf-waiting-room",
	"__cfwaitingroom", "just a moment...", "you are now in line", "attention required! | cloudflare",
	"performance &amp; security by cloudflare", "performance & security by cloudflare",
}

// AO3 layout markers (app/views/layouts/application.html.erb).
var ao3LayoutMarkers = []string{`id="outer"`, `id="main"`}

// workMarker must be on a work page; without it the page is parse_unexpected. Other kinds have
// no required marker (an empty listing has no blurbs, so the parser decides).
const workMarker = `id="workskin"`

// statusAO3SearchDown is what AO3 answers when Elasticsearch is unavailable (otwarchive
// ApplicationController: rescue_from ...ServiceUnavailable { head 445 }), instead of a 503 so
// it can tell the two apart. For us it is an outage: back off and retry.
const statusAO3SearchDown = 445

// Verdict is the classifier output with a content-free reason for logs.
type Verdict struct {
	Class  Class
	Reason string
}

// classifyRedirect handles 3xx responses that should not be followed.
func classifyRedirect(loc *url.URL) (Verdict, bool) {
	if loc == nil {
		return Verdict{}, false
	}
	if strings.HasPrefix(loc.Path, "/users/login") {
		if loc.Query().Get("restricted") == "true" {
			return Verdict{ClassRestricted, "redirect=login restricted=true"}, true
		}
		return Verdict{ClassHidden, "redirect=login access_denied"}, true
	}
	if loc.Path == "/" || loc.Path == "" {
		return Verdict{ClassHidden, "redirect=root access_denied"}, true
	}
	return Verdict{}, false
}

// classify decides the class of a final (non-redirect) response.
func classify(kind Kind, status int, body []byte) Verdict {
	lower := bytes.ToLower(body)
	var cfHits []string
	for _, m := range cfMarkers {
		if bytes.Contains(lower, []byte(m)) {
			cfHits = append(cfHits, m)
		}
	}
	cf := strings.Join(cfHits, ",")
	var missing []string
	for _, m := range ao3LayoutMarkers {
		if !bytes.Contains(body, []byte(m)) {
			missing = append(missing, m)
		}
	}

	switch {
	case status == http.StatusTooManyRequests:
		return Verdict{ClassRateLimited, "status=429"}
	case status >= 500:
		return Verdict{ClassUnavailable, "status=" + strconv.Itoa(status) + cfReason(cf)}
	case status == http.StatusNotFound:
		if kind == KindRobots {
			return Verdict{ClassNotFound, "status=404"}
		}
		if len(cfHits) > 0 {
			return Verdict{ClassUnavailable, "status=404" + cfReason(cf)}
		}
		// A 404 marks a work deleted, so only AO3's own 404 page (rendered in the site layout)
		// counts. A bare 404 from a proxy or a deploy is an outage, not a deletion.
		if len(missing) > 0 {
			return Verdict{ClassUnavailable, "status=404 missing_layout=" + strings.Join(missing, ",")}
		}
		return Verdict{ClassNotFound, "status=404"}
	case status == http.StatusForbidden: // Cloudflare blocks and challenges (5xx returned above)
		return Verdict{ClassUnavailable, "status=" + strconv.Itoa(status) + cfReason(cf)}
	case status == statusAO3SearchDown:
		return Verdict{ClassUnavailable, "status=" + strconv.Itoa(status) + " search_down"}
	case status >= 400:
		return Verdict{ClassUnexpected, "status=" + strconv.Itoa(status)}
	case status < 200 || status >= 300:
		return Verdict{ClassUnexpected, "status=" + strconv.Itoa(status)}
	}

	if kind == KindRobots {
		if len(cfHits) > 0 {
			return Verdict{ClassUnavailable, "status=200" + cfReason(cf)}
		}
		return Verdict{ClassOK, "status=200"}
	}
	if len(missing) > 0 {
		// Not an AO3 page: a Cloudflare challenge/waiting room or a maintenance page. Back off
		// rather than misparse.
		if len(cfHits) > 0 {
			return Verdict{ClassUnavailable, "status=200 cf_page" + cfReason(cf)}
		}
		return Verdict{ClassUnavailable, "status=200 missing_layout=" + strings.Join(missing, ",")}
	}
	// A real AO3 page. Cloudflare injects its detection script (challenge-platform) into normal
	// pages too; that is not a challenge, so it only shows up in the reason for debugging.
	okReason := "status=200"
	if len(cfHits) > 0 {
		okReason += " cf_injected=" + cf
	}
	if kind == KindWork && !bytes.Contains(body, []byte(workMarker)) {
		if bytes.Contains(body, []byte(`class="caution notice"`)) && bytes.Contains(body, []byte("view_adult=true")) {
			return Verdict{ClassAdultGate, okReason + " adult_interstitial"}
		}
		return Verdict{ClassUnexpected, okReason + " missing=" + workMarker}
	}
	return Verdict{ClassOK, okReason}
}

func cfReason(cf string) string {
	if cf == "" {
		return ""
	}
	return " cf_markers=" + cf
}

// ParseRetryAfter reads a Retry-After header in either form (delta-seconds or HTTP date). It
// returns 0 when the header is absent, invalid or already past. The AO3 client and the image
// worker both use it.
func ParseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}
