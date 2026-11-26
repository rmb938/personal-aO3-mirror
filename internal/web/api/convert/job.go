package convert

import (
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// Job maps one job.
func Job(j model.Job) dto.Job {
	out := dto.Job{
		ID:          j.ID,
		Kind:        dto.JobKind(j.Kind),
		Target:      j.Target,
		SourceID:    j.SourceID,
		Priority:    j.Priority,
		State:       dto.JobState(j.State),
		Attempts:    j.Attempts,
		MaxAttempts: j.MaxAttempts,
		NotBeforeAt: utc(j.NotBefore),
		CreatedAt:   utc(j.CreatedAt),
		StartedAt:   utc(j.StartedAt),
		FinishedAt:  utc(j.FinishedAt),
	}
	out.LastErrorCode, out.LastErrorDetail = splitError(j.LastError)
	return out
}

// JobList maps one page of jobs.
func JobList(items []model.Job, total, page, perPage int) dto.List[dto.Job] {
	return list(items, Job, total, page, perPage)
}
