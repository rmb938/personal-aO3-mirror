package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/api/request"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
)

// SourceAdd maps the outcome of the source add job jobID.
func SourceAdd(jobID int64, o controllers.SourceAddOutcome) dto.SourceAdd {
	out := dto.SourceAdd{JobID: jobID, State: dto.JobState(o.State)}
	out.LastErrorCode, out.LastErrorDetail = splitError(o.Error)
	if o.Result != nil {
		r := AddSourceResult(*o.Result)
		out.Result = &r
	}
	return out
}

// AddSourceResult maps what a source add created.
func AddSourceResult(r controllers.AddSourceResult) dto.SourceAddResult {
	return dto.SourceAddResult{Source: Source(r.Source), Blurbs: r.Blurbs, Estimate: estimate(r.Estimate)}
}

// AddSourceOptions maps a validated source add body to the controller's options (zero values
// keep the config defaults).
func AddSourceOptions(r request.SourceAdd) controllers.AddSourceOptions {
	return controllers.AddSourceOptions{Order: string(r.Order), Priority: r.Priority,
		PollInterval: r.PollInterval.Duration(), Confirm: r.Confirm}
}

// QueuedSourceAdd maps a queued source add (q.Added must be nil: a single work added at once
// is an AddSourceResult).
func QueuedSourceAdd(q controllers.QueuedSourceAdd) dto.SourceAddQueued {
	return dto.SourceAddQueued{JobID: q.JobID, Created: q.Created}
}

func estimate(e controllers.Estimate) dto.Estimate {
	return dto.Estimate{
		Total:           e.Total,
		Pages:           e.Pages,
		ListingRequests: e.ListingRequests,
		WorkFetches:     e.WorkFetches,
		Requests:        e.Requests,
		Duration:        dto.Duration(e.Duration),
		DiskBytes:       e.DiskBytes,
		Windowed:        e.Windowed,
	}
}
