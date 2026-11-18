package skeleton

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKey() *Skeletonizer { return NewWithKey([]byte("fixed test key")) }

func TestTextRules(t *testing.T) {
	s := testKey()
	for _, keep := range []string{"Words:", "Series Begun:", "Teen And Up Audiences", "F/M, Gen", "No category"} {
		assert.Equal(t, keep, s.Text(keep), "UI text must be kept")
	}
	got := s.Text("12,345")
	assert.Regexp(t, `^[1-9][0-9],[0-9]{3}$`, got, "number shape")
	assert.NotEqual(t, "12,345", got, "number must be replaced")
	assert.Regexp(t, `^20[0-9]{2}-[01][0-9]-[0-3][0-9]$`, s.Text("2026-10-01"), "iso date")
	assert.Regexp(t, `^[0-3][0-9] [A-Z][a-z]{2} 20[0-9]{2}$`, s.Text("01 Nov 2026"), "rfc date")
	for _, rel := range []string{"3 days ago", "1 hour ago", "less than 1 minute ago"} {
		assert.Equal(t, rel, s.Text(rel), "relative date")
	}
	assert.Regexp(t, `^[0-9]+ - [0-9]+ of [0-9],[0-9]{3} Works in$`, s.Text("21 - 40 of 1,234 Works in"), "listing heading")
	got = s.Text("123/?")
	assert.Regexp(t, `^[1-9][0-9]{2}/\?$`, got, "chapters")
	assert.NotEqual(t, "123/?", got, "chapters must be replaced")

	// consistency within a run (Text is stateful, so call it twice)
	first, second := s.Text("Secret Name"), s.Text("Secret Name")
	assert.Equal(t, first, second, "replacements must be consistent")
	assert.Equal(t, s.Number2("300")+"/"+s.Number2("300"), s.Text("300/300"), "replacements must be consistent")
	assert.NotContains(t, strings.ToLower(s.Text("Secret Name")), "secret", "word leaked")
	// different keys give different output
	assert.NotEqual(t, NewWithKey([]byte("a")).Text("Secret Name Here"), NewWithKey([]byte("b")).Text("Secret Name Here"),
		"output should depend on the key")
}

// Number2 exposes number for the consistency test.
func (s *Skeletonizer) Number2(v string) string { return s.number(v) }

func TestPatternConsistentWithText(t *testing.T) {
	s := testKey()
	require.Equal(t, "/tags/"+s.slug("Some%20Tag*s*Other")+"/works?page="+s.number("200"),
		s.Pattern("/tags/Some%20Tag*s*Other/works?page=200"), "tag pattern")
	// slug decodes back to the lorem'd tag name, matching link text
	dec, err := url.PathUnescape(s.slug("Some%20Tag*s*Other"))
	require.NoError(t, err)
	assert.Equal(t, s.Text("Some Tag/Other"), strings.ReplaceAll(dec, "*s*", "/"), "slug consistent with text")
	assert.Equal(t, "/works/"+s.number("123")+"/chapters/"+s.number("456"), s.Pattern("/works/123/chapters/456"), "work path")
	assert.Equal(t, "//external/x", s.Pattern("https://images.example.invalid/a.png"), "external")
	assert.Equal(t, "work_"+s.number("123"), s.maskID("work_123"), "id")
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
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(out), Header), "skeleton must start with the generated-file header")
	s := strings.ToLower(string(out))
	require.NotContains(t, s, "secret", "skeleton leaked content")
	for _, keep := range []string{`id="main"`, `class="works-show"`, `class="caution notice"`, `class="userstuff"`, `src="//external/x"`, `title="mature"`} {
		assert.Contains(t, s, keep, "skeleton lost a structural attribute")
	}
}
