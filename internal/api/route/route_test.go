package route

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPath(t *testing.T) {
	assert.Equal(t, "/api/v1/sources", Path(Sources))
	assert.Equal(t, "/api/v1/sources/3", Path(Source, 3))
	assert.Equal(t, "/api/v1/source-adds/12", Path(SourceAdd, 12))
	assert.Equal(t, "/a/1/b/2", Path("/a/{x}/b/{y}", 1, 2))
	assert.Panics(t, func() { Path(Source) }, "too few ids")
	assert.Panics(t, func() { Path(Source, 1, 2) }, "too many ids")
	assert.Panics(t, func() { Path(Sources, 1) }, "ids for a route without params")
}
