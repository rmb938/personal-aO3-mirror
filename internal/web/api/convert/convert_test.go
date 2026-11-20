package convert

import (
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/jobs"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// The model-side value lists must be exactly the dto enum sets: a value added on one side
// only would come out hashed (or be dead in the API contract).
func TestEnumSetsMatchModel(t *testing.T) {
	var states, kinds []string
	for _, s := range model.SourceStates() {
		states = append(states, string(s))
	}
	for _, k := range ao3url.Kinds() {
		kinds = append(kinds, string(k))
	}
	assert.ElementsMatch(t, states, dto.SourceState("").EnumValues(), "source states")
	assert.ElementsMatch(t, model.JobStates(), dto.JobState("").EnumValues(), "job states")
	assert.ElementsMatch(t, kinds, dto.SourceKind("").EnumValues(), "source kinds")
	assert.ElementsMatch(t, jobs.Kinds(), dto.JobKind("").EnumValues(), "job kinds")
	assert.Equal(t, model.JobQueued, string(dto.JobQueued))
	assert.ElementsMatch(t, ao3url.OrderNames(), dto.Order("").EnumValues(), "orders")
	assert.ElementsMatch(t, controllers.JobErrorCodes(), dto.JobErrorCode("").EnumValues(), "job error codes")
	assert.ElementsMatch(t, strs(model.DaemonPhases()), dto.DaemonPhase("").EnumValues(), "daemon phases")
	assert.ElementsMatch(t, strs(model.WorkStatuses()), dto.WorkStatus("").EnumValues(), "work statuses")
	assert.ElementsMatch(t, store.ImageStatuses(), dto.ImageStatus("").EnumValues(), "image statuses")
	assert.ElementsMatch(t, strs(httpx.Classes()), dto.RequestClass("").EnumValues(), "request classes")
	assert.ElementsMatch(t, strs(httpx.Kinds()), dto.RequestKind("").EnumValues(), "request kinds")
	assert.ElementsMatch(t, model.JobEvents(), dto.JobEventType("").EnumValues(), "job events")
	assert.ElementsMatch(t, strs(store.ContentChanges()), dto.ContentChange("").EnumValues(), "content changes")
	assert.ElementsMatch(t, strs(model.TagTypes()), dto.TagType("").EnumValues(), "tag types")
	assert.ElementsMatch(t, slices.Collect(maps.Keys(config.TagInfoTypes)), dto.TagInfoType("").EnumValues(),
		"tag info types")
}

// strs converts a list of string-kind values.
func strs[S ~string](vs []S) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	return out
}

func TestSplitError(t *testing.T) {
	for _, c := range []struct {
		in           string
		code, detail string
	}{
		{"", "", ""},
		{"not_found", "not_found", ""},
		{"source_exists:12", "source_exists", "12"},
		{"internal:a: b", "internal", "a: b"},
	} {
		code, detail := splitError(c.in)
		assert.Equal(t, dto.JobErrorCode(c.code), code, c.in)
		assert.Equal(t, c.detail, detail, c.in)
	}
}

func TestConvPtr(t *testing.T) {
	double := func(n int) int { return 2 * n }
	assert.Nil(t, convPtr(nil, double))
	assert.Equal(t, new(0), convPtr(new(0), double), "zero is kept")
	assert.Equal(t, new(4), convPtr(new(2), double))

	assert.Nil(t, enumPtr[dto.JobState](nil))
	assert.Equal(t, new(dto.JobState("done")), enumPtr[dto.JobState](new("done")))
}

func TestConvSlice(t *testing.T) {
	double := func(n int) int { return 2 * n }
	assert.Nil(t, convSlice(nil, double))
	assert.Nil(t, convSlice([]int{}, double), "empty gives nil (written as [])")
	assert.Equal(t, []int{2, 4}, convSlice([]int{1, 2}, double))
}
