// Package fakeao3 is a local stand-in for AO3 used by tests and `ao3mirror dev fake-server`.
// It serves ONLY synthetic content. Never put real AO3 pages here (see CLAUDE.md).
package fakeao3

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

// Robots mirrors the structure of AO3's public robots.txt `User-agent: *` group (rules only).
const Robots = `User-agent:	*
Disallow: /works? # cruel but efficient
Disallow: /autocomplete/
Disallow: /downloads/
Disallow: /external_works/
Disallow: /bookmarks/search?
Disallow: /people/search?
Disallow: /tags/search?
Disallow: /works/search?

User-agent: GPTBot
Disallow: /
`

// Reply is one canned response.
type Reply struct {
	Status int
	Header map[string]string
	Body   string
}

// Recorded is one request the server saw.
type Recorded struct {
	Time      time.Time
	Method    string
	URI       string // path + raw query
	UserAgent string
	Cookie    string
}

// Server is an httptest server with scripted routes.
type Server struct {
	*httptest.Server
	mu       sync.Mutex
	robots   []Reply
	routes   map[string][]Reply // key: request URI (path?query) or bare path; replies consumed in order, last repeats
	requests []Recorded
}

func New() *Server {
	s := &Server{routes: map[string][]Reply{}, robots: []Reply{{Status: 200, Body: Robots}}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Handle scripts replies for uri ("/works/1?view_full_work=true" or "/works/1").
func (s *Server) Handle(uri string, replies ...Reply) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[uri] = append([]Reply(nil), replies...)
}

// SetRobots scripts robots.txt replies.
func (s *Server) SetRobots(replies ...Reply) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.robots = append([]Reply(nil), replies...)
}

// Requests returns a copy of all recorded requests.
func (s *Server) Requests() []Recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Recorded(nil), s.requests...)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, Recorded{Time: time.Now(), Method: r.Method, URI: r.URL.RequestURI(), UserAgent: r.UserAgent(), Cookie: r.Header.Get("Cookie")})
	var rep Reply
	switch {
	case r.URL.Path == "/robots.txt":
		rep = pop(&s.robots)
	case s.routes[r.URL.RequestURI()] != nil:
		q := s.routes[r.URL.RequestURI()]
		rep = pop(&q)
		s.routes[r.URL.RequestURI()] = q
	case s.routes[r.URL.EscapedPath()] != nil:
		q := s.routes[r.URL.EscapedPath()]
		rep = pop(&q)
		s.routes[r.URL.EscapedPath()] = q
	case s.routes[r.URL.Path] != nil:
		q := s.routes[r.URL.Path]
		rep = pop(&q)
		s.routes[r.URL.Path] = q
	default:
		rep = Reply{Status: 404, Body: Page(`<h2 class="heading">Error 404</h2>`)}
	}
	s.mu.Unlock()
	for k, v := range rep.Header {
		w.Header().Set(k, v)
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	if rep.Status == 0 {
		rep.Status = 200
	}
	w.WriteHeader(rep.Status)
	fmt.Fprint(w, rep.Body)
}

func pop(q *[]Reply) Reply {
	if len(*q) == 0 {
		return Reply{Status: 404}
	}
	r := (*q)[0]
	if len(*q) > 1 {
		*q = (*q)[1:]
	}
	return r
}

// Page wraps inner HTML in the AO3 layout skeleton (#outer, #inner, #main), per
// app/views/layouts/application.html.erb.
func Page(inner string) string {
	return `<!DOCTYPE html><html><head><title>Fixture</title></head><body class="logged-out">` +
		`<div id="outer" class="wrapper"><div id="inner" class="wrapper">` +
		`<div id="main" class="region" role="main">` + inner + `</div></div></div></body></html>`
}

// WorkPage is a minimal synthetic work page containing body (fake text only).
func WorkPage(body string) string {
	return Page(`<div class="wrapper"><dl class="work meta group"></dl></div>` +
		`<div id="workskin"><div class="preface group"><h2 class="title heading">Fixture Title</h2></div>` +
		`<div class="userstuff">` + body + `</div></div>`)
}

// AdultGate mimics app/views/works/_adult.html.erb.
func AdultGate(workID int) string {
	return Page(fmt.Sprintf(`<h2 class="landmark heading">Adult Content Warning</h2>`+
		`<p class="caution notice">fixture caution</p><ul class="actions">`+
		`<li><a href="/works/%d?view_adult=true">Proceed</a></li></ul>`, workID))
}

// CloudflareChallenge mimics an edge challenge page (no AO3 layout).
const CloudflareChallenge = `<!DOCTYPE html><html><head><title>Just a moment...</title></head>` +
	`<body><div id="challenge-platform"></div><script src="/cdn-cgi/challenge-platform/h/b/orchestrate"></script></body></html>`

// Cloudflare525 mimics the origin SSL handshake error page seen during the Nov 2026 incident.
const Cloudflare525 = `<!DOCTYPE html><html><head><title>SSL handshake failed</title></head>` +
	`<body><div id="cf-wrapper"><div id="cf-error-details">Error 525</div>` +
	`<span>Performance &amp; security by Cloudflare</span></div></body></html>`

// Maintenance mimics a non-AO3 downtime page (no layout markers).
const Maintenance = `<!DOCTYPE html><html><head><title>Down</title></head><body><p>fixture maintenance</p></body></html>`
