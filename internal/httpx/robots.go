package httpx

import (
	"time"

	"github.com/temoto/robotstxt"
)

// ProductToken is the robots.txt user-agent token we match groups against.
const ProductToken = "personal-ao3-mirror"

const (
	robotsTTL      = 24 * time.Hour
	robotsErrorTTL = 15 * time.Minute // retry sooner after 5xx/Cloudflare robots failures
)

type robotsCache struct {
	data    *robotstxt.RobotsData
	fetched time.Time
	ttl     time.Duration
	status  int
}

func (r *robotsCache) fresh(now time.Time) bool {
	return r != nil && r.data != nil && now.Sub(r.fetched) < r.ttl
}

// newRobots builds a cache entry. Per the robots spec (and temoto/robotstxt): 2xx parses the
// file, 4xx allows everything, 5xx disallows everything until the next refresh.
func newRobots(status int, body []byte, now time.Time, unavailable bool) (*robotsCache, error) {
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
	return &robotsCache{data: data, fetched: now, ttl: ttl, status: status}, nil
}

// allowed reports whether target (escaped path + optional ?query) may be fetched.
// TestAgent honours the allow-all (4xx) and disallow-all (5xx) states.
func (r *robotsCache) allowed(target string) bool {
	return r.data.TestAgent(target, ProductToken)
}
