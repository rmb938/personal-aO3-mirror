package ao3url

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// goTypeName is the content-free extra a malformed-URL error may carry (the parse error's type).
var goTypeName = regexp.MustCompile(`^\*?[a-z]+\.[A-Za-z]+$`)

// FuzzParse checks Parse on any input: a rejection is a URLError with a known reason whose
// message is only that reason's fixed text (it never quotes the URL), and an accepted URL is a
// known kind whose canonical path (BasePath) parses back to the same source.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"https://archiveofourown.org/tags/Fixture%20Tag*s*Other/works?work_search[sort_column]=kudos_count",
		"/users/fixture_author/pseuds/Fixture%20Pseud/works", "/users/fixture_author/bookmarks", "/users/fixture_author",
		"/collections/fixture_collection", "/series/12", "/works/34/chapters/5", "/chapters/5", "/works?tag_id=1",
		"/works/search?q=x", "https://example.com/tags/x", "tags/x", "/tags/%zz", "/users/a%2Fb/pseuds/c",
		"/tags/x/bookmarks", "/collections/x/profile", "/users/x/pseuds/y/bookmarks", "http://[::1", " /works/1 ",
		"//archiveofourown.org/works/1", "/tags/*d**d*", "/works/-1", "/works/99999999999999999999",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		s, err := Parse(raw)
		if err != nil {
			requireContentFree(t, err)
			return
		}
		require.Contains(t, Kinds(), s.Kind)
		require.NotEmpty(t, s.Target)
		p, err := BasePath(s)
		require.NoError(t, err)
		back, err := Parse(p)
		require.NoError(t, err, "BasePath %q", p)
		require.Equal(t, s.Kind, back.Kind)
		if !strings.Contains(s.Target, "*") { // TagParam leaves '*', so "*s*" in a name is ambiguous, as on AO3
			require.Equal(t, s.Target, back.Target)
		}
	})
}

func requireContentFree(t *testing.T, err error) {
	t.Helper()
	ue, ok := errors.AsType[*URLError](err)
	require.True(t, ok, "want *URLError, got %T", err)
	require.True(t, slices.Contains(Reasons(), ue.Reason), "reason %q", ue.Reason)
	msg := ue.base().Error() + ": " + ue.Reason.Hint()
	got := err.Error()
	if got != msg {
		extra, ok := strings.CutPrefix(got, msg+" (")
		require.True(t, ok, "message %q", got)
		extra, ok = strings.CutSuffix(extra, ")")
		require.True(t, ok && goTypeName.MatchString(extra), "message extra %q", extra)
	}
}
