package searchquery

import (
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/require"
)

// FuzzTerms checks Terms on any input: every term is non-empty normalized text (single spaces,
// no quote, something the index can match), and only words of q appear in it.
func FuzzTerms(f *testing.F) {
	for _, s := range []string{
		"", "fixture", `fixture -lorem "ipsum  dolor" -"sit amet"`, `"unclosed phrase`, `-`, `--x`, `a"b"c`,
		`""`, `- "`, "NEAR(a b) OR c* ^d AND {e}:f", " x y", "\xff\xfe", "-\"\x00\"", "é ü 中文",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, q string) {
		terms := Terms(q)
		// Every word of every term is a field of q once quotes and a leading '-' are split off.
		words := strings.FieldsFunc(q, func(r rune) bool { return unicode.IsSpace(r) || r == '"' })
		for _, term := range terms {
			require.NotEmpty(t, term.Text)
			require.NotContains(t, term.Text, `"`)
			require.Equal(t, strings.Join(strings.Fields(term.Text), " "), term.Text, "normalized spaces")
			require.GreaterOrEqual(t, strings.IndexFunc(term.Text, isTokenRune), 0, "has a token rune")
			for w := range strings.FieldsSeq(term.Text) {
				require.True(t, containsWord(words, w), "word %q comes from q", w)
			}
		}
	})
}

// containsWord reports whether w is one of words, or one of them without leading '-'s.
func containsWord(words []string, w string) bool {
	for _, x := range words {
		for {
			if x == w {
				return true
			}
			var ok bool
			if x, ok = strings.CutPrefix(x, "-"); !ok {
				break
			}
		}
	}
	return false
}
