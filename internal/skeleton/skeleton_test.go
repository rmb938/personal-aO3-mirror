package skeleton

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func testKey() *Skeletonizer { return NewWithKey([]byte("fixed test key")) }

func TestTextRules(t *testing.T) {
	s := testKey()
	for _, keep := range []string{"Words:", "Series Begun:", "Teen And Up Audiences", "F/M, Gen", "No category"} {
		if got := s.Text(keep); got != keep {
			t.Errorf("UI text %q changed to %q", keep, got)
		}
	}
	if got := s.Text("12,345"); !regexp.MustCompile(`^[1-9][0-9],[0-9]{3}$`).MatchString(got) || got == "12,345" {
		t.Errorf("number shape: %q", got)
	}
	if got := s.Text("2026-10-01"); !regexp.MustCompile(`^20[0-9]{2}-[01][0-9]-[0-3][0-9]$`).MatchString(got) {
		t.Errorf("iso date: %q", got)
	}
	if got := s.Text("01 Nov 2026"); !regexp.MustCompile(`^[0-3][0-9] [A-Z][a-z]{2} 20[0-9]{2}$`).MatchString(got) {
		t.Errorf("rfc date: %q", got)
	}
	if got := s.Text("3 days"); got != "3 days" {
		t.Errorf("relative date: %q", got)
	}
	if got := s.Text("21 - 40 of 1,234 Works in"); !regexp.MustCompile(`^[0-9]+ - [0-9]+ of [0-9],[0-9]{3} Works in$`).MatchString(got) {
		t.Errorf("listing heading: %q", got)
	}
	if got := s.Text("123/?"); !regexp.MustCompile(`^[1-9][0-9]{2}/\?$`).MatchString(got) || got == "123/?" {
		t.Errorf("chapters: %q", got)
	}
	// consistency within a run
	if s.Text("Secret Name") != s.Text("Secret Name") || s.Text("300/300") != s.Number2("300")+"/"+s.Number2("300") {
		t.Error("replacements must be consistent")
	}
	if strings.Contains(strings.ToLower(s.Text("Secret Name")), "secret") {
		t.Error("word leaked")
	}
	// different keys give different output
	if NewWithKey([]byte("a")).Text("Secret Name Here") == NewWithKey([]byte("b")).Text("Secret Name Here") {
		t.Error("output should depend on the key")
	}
}

// Number2 exposes number for the consistency test.
func (s *Skeletonizer) Number2(v string) string { return s.number(v) }

func TestPatternConsistentWithText(t *testing.T) {
	s := testKey()
	tag := s.Pattern("/tags/Some%20Tag*s*Other/works?page=200")
	want := "/tags/" + s.slug("Some%20Tag*s*Other") + "/works?page=" + s.number("200")
	if tag != want {
		t.Fatalf("tag pattern %q want %q", tag, want)
	}
	// slug decodes back to the lorem'd tag name, matching link text
	dec, _ := url.PathUnescape(s.slug("Some%20Tag*s*Other"))
	if strings.ReplaceAll(dec, "*s*", "/") != s.Text("Some Tag/Other") {
		t.Errorf("slug %q not consistent with text %q", dec, s.Text("Some Tag/Other"))
	}
	if got := s.Pattern("/works/123/chapters/456"); got != "/works/"+s.number("123")+"/chapters/"+s.number("456") {
		t.Errorf("work path: %q", got)
	}
	if got := s.Pattern("https://images.example.invalid/a.png"); got != "//external/x" {
		t.Errorf("external: %q", got)
	}
	if got := s.maskID("work_123"); got != "work_"+s.number("123") {
		t.Errorf("id: %q", got)
	}
}

func TestSkeletonizeDropsContent(t *testing.T) {
	in := `<html><head><title>Secret Title</title><style>.secret{}</style><script>var secret=1</script></head>
<body><div id="main" class="works-show" title="secret tooltip" data-x="secret">
<p class="caution notice">secret text <!-- secret comment --></p>
<div class="userstuff"><p class="author-secret" id="secretid">secret body <b>more</b></p></div>
<img src="https://example.invalid/secret.png" alt="secret alt">
<a href="/users/secretuser/pseuds/secretpseud">secretpseud (secretuser)</a>
<span id="secretAuthorId" title="Mature">s</span>
</div></body></html>`
	out, err := Skeletonize(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), Header) {
		t.Error("skeleton must start with the generated-file header")
	}
	s := strings.ToLower(string(out))
	if strings.Contains(s, "secret") {
		t.Fatalf("skeleton leaked content:\n%s", out)
	}
	for _, keep := range []string{`id="main"`, `class="works-show"`, `class="caution notice"`, `class="userstuff"`, `src="//external/x"`, `title="mature"`} {
		if !strings.Contains(s, keep) {
			t.Errorf("skeleton lost %s:\n%s", keep, out)
		}
	}
}
