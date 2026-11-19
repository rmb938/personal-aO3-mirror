package request

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/api/problem"
)

// errorsOf validates s and returns the recorded items as pointer → detail message.
func errorsOf(t *testing.T, s Request) map[string]string {
	t.Helper()
	var v problem.Validation
	s.Validate(&v)
	out := map[string]string{}
	if v.Len() == 0 {
		return out
	}
	pe, ok := errors.AsType[*problem.Error](v.Err())
	require.True(t, ok)
	for _, it := range pe.Errors {
		assert.Empty(t, it.Param)
		out[it.Pointer] = it.Detail.Message()
	}
	return out
}

func urlHint(t *testing.T, r ao3url.Reason) string {
	t.Helper()
	d, ok := problem.URLDetail(&ao3url.URLError{Reason: r})
	require.True(t, ok)
	return d.Message()
}

func TestSourceAddValid(t *testing.T) {
	for _, s := range []SourceAdd{
		{URL: "/tags/a/works"},
		{URL: "https://archiveofourown.org/works/1"},
		{URL: "/tags/a/works", Order: "kudos_desc", Priority: -MaxSourcePriority, PollInterval: dto.Duration(MinPollInterval)},
		{URL: "/tags/a/works", Priority: MaxSourcePriority, PollInterval: dto.Duration(MaxPollInterval)},
		{URL: "/tags/a/works", Confirm: true}, // the listener check is the handler's
	} {
		assert.Empty(t, errorsOf(t, &s), "%+v", s)
	}
}

func TestSourceAddInvalid(t *testing.T) {
	long := "/tags/" + strings.Repeat("a", MaxSourceURLBytes) + "/works"
	tag := "/tags/a/works"
	for name, tc := range map[string]struct {
		in   SourceAdd
		want map[string]string
	}{
		"no url":         {SourceAdd{}, map[string]string{"/url": detailSourceURLLength.Message()}},
		"long url":       {SourceAdd{URL: long}, map[string]string{"/url": detailSourceURLLength.Message()}},
		"control char":   {SourceAdd{URL: "/tags/a\u0007b/works"}, map[string]string{"/url": problem.DetailText.Message()}},
		"invalid utf8":   {SourceAdd{URL: "/tags/\xff/works"}, map[string]string{"/url": problem.DetailText.Message()}},
		"work search":    {SourceAdd{URL: "/works/search?q=x"}, map[string]string{"/url": urlHint(t, ao3url.ReasonWorkSearch)}},
		"not ao3":        {SourceAdd{URL: "https://example.com/tags/x/works"}, map[string]string{"/url": urlHint(t, ao3url.ReasonNotAO3)}},
		"order":          {SourceAdd{URL: tag, Order: "x"}, map[string]string{"/order": detailOrder.Message()}},
		"priority low":   {SourceAdd{URL: tag, Priority: -MaxSourcePriority - 1}, map[string]string{"/priority": detailPriority.Message()}},
		"priority high":  {SourceAdd{URL: tag, Priority: MaxSourcePriority + 1}, map[string]string{"/priority": detailPriority.Message()}},
		"interval short": {SourceAdd{URL: tag, PollInterval: dto.Duration(MinPollInterval - time.Second)}, map[string]string{"/poll_interval": detailPollInterval.Message()}},
		"interval long":  {SourceAdd{URL: tag, PollInterval: dto.Duration(MaxPollInterval + time.Second)}, map[string]string{"/poll_interval": detailPollInterval.Message()}},
		"negative":       {SourceAdd{URL: tag, PollInterval: dto.Duration(-time.Hour)}, map[string]string{"/poll_interval": detailPollInterval.Message()}},
		"all at once": {SourceAdd{URL: "/chapters/5", Order: "x", Priority: 5000, PollInterval: dto.Duration(time.Second)},
			map[string]string{"/url": urlHint(t, ao3url.ReasonChapter), "/order": detailOrder.Message(),
				"/priority": detailPriority.Message(), "/poll_interval": detailPollInterval.Message()}},
	} {
		assert.Equal(t, tc.want, errorsOf(t, &tc.in), name)
	}
}

// Every listed type is a Request through its pointer (what decodeJSON takes).
func TestTypesAreRequests(t *testing.T) {
	for _, v := range Types() {
		typ := reflect.TypeOf(v)
		assert.Equal(t, reflect.Struct, typ.Kind(), "%s: list the struct value", typ)
		assert.True(t, reflect.PointerTo(typ).Implements(reflect.TypeFor[Request]()), "%s", typ)
	}
}
