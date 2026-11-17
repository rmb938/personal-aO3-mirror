package controllers

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
)

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
		// The httpx classifier's own "parse_unexpected" class counts for a 200 page (blocks
		// backfill), not for an odd answer such as an unusual 4xx or an unfollowed redirect.
		{outcome(&httpx.Response{Class: httpx.ClassUnexpected, Status: 200}), true},
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
