package controllers

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
)

// A WaitError (nothing sent, the job is requeued without an attempt) never counts as giving up,
// even on the last attempt; a permanent error or the last retryable one does.
func TestGiveUpInfoIgnoresWait(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		err   error
		final bool
		mark  bool
	}{
		{WaitError{Code: CodeRobotsUnavailable}, true, false},
		{WaitError{Code: CodeRobotsUnavailable}, false, false},
		{RetryError{Code: "unavailable"}, false, false},
		{RetryError{Code: "unavailable"}, true, true},
		{PermanentError{Code: CodeTooLarge}, false, true},
	} {
		marked := false
		err := giveUpInfo(ctx, c.err, c.final, func() error { marked = true; return nil })
		require.ErrorIs(t, err, c.err)
		require.Equal(t, c.mark, marked, "%v final=%t", c.err, c.final)
	}
}

func TestPermanentErrorCodes(t *testing.T) {
	// Error() is what lands in job last_error: "code" or "code:detail".
	require.Equal(t, "no_source", PermanentError{Code: CodeNoSource}.Error())
	require.Equal(t, "parse_unexpected:#main,dl.stats", parseUnexpected([]string{"#main", "dl.stats"}).Error())

	for _, c := range []struct {
		err  error
		want bool
	}{
		{parseUnexpected([]string{"#main"}), true},
		{PermanentError{Code: CodeParseError}, true},
		{fmt.Errorf("wrapped: %w", PermanentError{Code: CodeParseError}), true},
		// For listing, series and tag pages the classifier's "parse_unexpected" class is only
		// ever an odd answer (an unusual 4xx, an unfollowed redirect), never a parse failure.
		{outcome(&httpx.Response{Class: httpx.ClassUnexpected, Status: 410}), false},
		{outcome(&httpx.Response{Class: httpx.ClassUnexpected, Status: 302}), false},
		{outcome(&httpx.Response{Class: httpx.ClassNotFound}), false},
		{outcome(&httpx.Response{Class: httpx.ClassUnavailable}), false},
		{PermanentError{Code: CodeBadCursor}, false},
		{nil, false},
	} {
		require.Equal(t, c.want, IsParseFailure(c.err), "%v", c.err)
	}
}
