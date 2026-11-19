package convert

import "github.com/rmb938/personal-aO3-mirror/internal/api/dto"

// SourceCancelled maps the result of controllers.CancelSource: how many queued jobs it dropped.
func SourceCancelled(droppedJobs int64) dto.SourceCancelled {
	return dto.SourceCancelled{DroppedJobs: droppedJobs}
}
