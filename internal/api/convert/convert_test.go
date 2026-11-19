package convert

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
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
	assert.ElementsMatch(t, ao3url.OrderNames(), dto.Order("").EnumValues(), "orders")
	assert.ElementsMatch(t, controllers.JobErrorCodes(), dto.JobErrorCode("").EnumValues(), "job error codes")
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
