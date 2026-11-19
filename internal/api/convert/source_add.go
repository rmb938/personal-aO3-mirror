package convert

import (
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
)

// SourceAdd maps the outcome of the source add job jobID.
func SourceAdd(jobID int64, o controllers.SourceAddOutcome) dto.SourceAdd {
	out := dto.SourceAdd{JobID: jobID, State: dto.JobState(o.State)}
	out.LastErrorCode, out.LastErrorDetail = splitError(o.Error)
	if o.Result != nil {
		out.Result = &dto.SourceAddResult{
			Source:   Source(o.Result.Source),
			Blurbs:   o.Result.Blurbs,
			Estimate: estimate(o.Result.Estimate),
		}
	}
	return out
}

func estimate(e controllers.Estimate) dto.Estimate {
	return dto.Estimate{
		Total:           e.Total,
		Pages:           e.Pages,
		ListingRequests: e.ListingRequests,
		WorkFetches:     e.WorkFetches,
		Requests:        e.Requests,
		DurationS:       int64(e.Duration / time.Second),
		DiskBytes:       e.DiskBytes,
		Windowed:        e.Windowed,
	}
}
