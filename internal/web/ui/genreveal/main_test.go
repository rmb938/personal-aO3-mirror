package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

// The committed file must match what go generate would write, so a renamed param can't leave
// the UI sending the old one.
func TestGeneratedFileIsCurrent(t *testing.T) {
	got, err := os.ReadFile("../../../../web/src/lib/reveal.gen.ts")
	require.NoError(t, err)
	assert.Equal(t, source(), string(got), "web/src/lib/reveal.gen.ts is stale: run `make generate`")
}

func TestSource(t *testing.T) {
	src := source()
	assert.True(t, strings.HasPrefix(src, "// Code generated "), "marks the file as generated")
	assert.Contains(t, src, `export const REVEAL_PARAM = "`+runtime.RevealParam+`";`)
	assert.Contains(t, src, `export const REVEAL_VALUE = "`+runtime.RevealValue+`";`)
}
