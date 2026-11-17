package httpx

import (
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
// the next refresh (which comes sooner). unavailable marks a challenge page served with a
// non-5xx status, which is treated as a server error.
func NewRobots(status int, body []byte, now time.Time, unavailable bool) (*Robots, error) {
	if unavailable && status < 500 {
		status = 503 // Cloudflare challenge served as 200/403: treat as server error => disallow all
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

// Allowed reports whether u (escaped path + query) may be fetched by ProductToken.
// TestAgent honours the allow-all (4xx) and disallow-all (5xx) states.
func (r *Robots) Allowed(u *url.URL) bool {
	target := u.EscapedPath()
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	return r.data.TestAgent(target, ProductToken)
}
