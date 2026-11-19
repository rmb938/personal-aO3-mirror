package convert

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
)

func TestSourceCancelled(t *testing.T) {
	assert.Equal(t, dto.SourceCancelled{DroppedJobs: 3}, SourceCancelled(3))
}
