package convert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

func TestSourceAdd(t *testing.T) {
	got := SourceAdd(9, controllers.SourceAddOutcome{State: model.JobFailed, Error: "source_exists:12"})
	assert.Equal(t, dto.SourceAdd{JobID: 9, State: "failed", LastErrorCode: "source_exists", LastErrorDetail: "12"}, got)

	got = SourceAdd(9, controllers.SourceAddOutcome{State: model.JobDone, Result: &controllers.AddSourceResult{
		Source: model.Source{ID: 3, Kind: "work", Target: "8"}, Blurbs: 20,
		Estimate: controllers.Estimate{Total: 1, Pages: 2, ListingRequests: 3, WorkFetches: 4, Requests: 5,
			Duration: 90 * time.Second, DiskBytes: 6, Windowed: true},
	}})
	require.NotNil(t, got.Result)
	assert.Empty(t, got.LastErrorCode)
	assert.Equal(t, int64(3), got.Result.Source.ID)
	assert.Equal(t, 20, got.Result.Blurbs)
	assert.Equal(t, dto.Estimate{Total: 1, Pages: 2, ListingRequests: 3, WorkFetches: 4, Requests: 5, Duration: dto.Duration(90 * time.Second),
		DiskBytes: 6, Windowed: true}, got.Result.Estimate)
}
