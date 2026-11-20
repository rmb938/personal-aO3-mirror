package convert

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestSourceRefreshed(t *testing.T) {
	assert.Equal(t, dto.SourceRefreshed{PollNow: true, RequeuedWorks: 2, RescanStarted: true, UnblockedParser: true},
		SourceRefreshed(controllers.RefreshSourceResult{PollNow: true, RequeuedWorks: 2, RescanStarted: true, UnblockedParser: true}))
	assert.Equal(t, dto.SourceRefreshed{PollNow: true}, SourceRefreshed(controllers.RefreshSourceResult{PollNow: true}))
}
