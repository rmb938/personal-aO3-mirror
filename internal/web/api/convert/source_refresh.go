package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// SourceRefreshed maps what controllers.RefreshSource scheduled.
func SourceRefreshed(r controllers.RefreshSourceResult) dto.SourceRefreshed {
	return dto.SourceRefreshed{PollNow: r.PollNow, RequeuedWorks: r.RequeuedWorks, RescanStarted: r.RescanStarted,
		UnblockedParser: r.UnblockedParser}
}
