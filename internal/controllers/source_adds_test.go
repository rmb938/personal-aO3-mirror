package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// existingSourceID reads back what sourceAddError writes for an ExistsError.
func TestExistingSourceID(t *testing.T) {
	id, err := existingSourceID(sourceAddError(ExistsError{ID: 12}).Error())
	require.NoError(t, err)
	assert.Equal(t, int64(12), id)

	for _, other := range []string{"", "not_found", "not_found:12", "source_existsx:12"} {
		id, err := existingSourceID(other)
		require.NoError(t, err, other)
		assert.Zero(t, id, other)
	}
	for _, bad := range []string{"source_exists", "source_exists:", "source_exists:x", "source_exists:0", "source_exists:-3"} {
		_, err := existingSourceID(bad)
		assert.Error(t, err, bad)
	}
}
