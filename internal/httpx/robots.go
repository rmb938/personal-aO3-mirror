package httpx

import (
	"net/http"
	"net/url"
	"time"

	"github.com/temoto/robotstxt"
)

// ProductToken is the robots.txt user-agent token we match groups against.
const ProductToken = "personal-ao3-mirror" //nolint:gosec // G101: robots.txt user-agent name, not a credential

const (
	robotsTTL      = 24 * time.Hour
	robotsErrorTTL = 15 * time.Minute // retry sooner after 5xx/Cloudflare robots failures
)

// Robots is one host's parsed robots.txt and how long it stays valid. The AO3 client and the
// image worker both use it.
type Robots struct {
	data    *robotstxt.RobotsData
	fetched time.Time
	ttl     time.Duration
	status  int
}

// NewRobots builds a cache entry from a robots.txt response. Per the robots spec (and
// temoto/robotstxt): 2xx parses the file, 4xx allows everything, 5xx disallows everything until
// the next refresh (which comes sooner). A 429 is the server being busy, not "no robots.txt",
// so it counts as a 5xx too (RFC 9309). unavailable marks a challenge page served with a
// non-5xx status, which is treated as a server error.
func NewRobots(status int, body []byte, now time.Time, unavailable bool) (*Robots, error) {
	if (unavailable && status < 500) || status == http.StatusTooManyRequests {
		status = http.StatusServiceUnavailable // disallow all, refresh after robotsErrorTTL
	}
	data, err := robotstxt.FromStatusAndBytes(status, body)
	if err != nil {
		return nil, err
	}
	ttl := robotsTTL
	if status >= 500 {
		ttl = robotsErrorTTL
	}
	return &Robots{data: data, fetched: now, ttl: ttl, status: status}, nil
}

// Fresh reports whether the entry is still valid at now (false for a nil entry).
func (r *Robots) Fresh(now time.Time) bool {
	return r != nil && r.data != nil && now.Sub(r.fetched) < r.ttl
}

// Status is the effective HTTP status the entry was built from.
func (r *Robots) Status() int { return r.status }

// Unavailable reports whether the entry stands for a robots.txt that could not be read (5xx,
// 429, challenge): everything is disallowed only until the next refresh, so a refusal from it
// is temporary.
func (r *Robots) Unavailable() bool { return r.status >= 500 }

// ExpiresAt is when the entry stops being Fresh and robots.txt is read again.
func (r *Robots) ExpiresAt() time.Time { return r.fetched.Add(r.ttl) }

// Allowed reports whether u (escaped path + query) may be fetched by ProductToken.
// TestAgent honours the allow-all (4xx) and disallow-all (5xx) states.
func (r *Robots) Allowed(u *url.URL) bool {
	target := u.EscapedPath()
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	return r.data.TestAgent(target, ProductToken)
}
