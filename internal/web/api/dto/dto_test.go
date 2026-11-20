package dto

import (
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDuration(t *testing.T) {
	type body struct {
		D Duration `json:"d"`
	}
	b, err := json.Marshal(body{D: Duration(6*time.Hour + 30*time.Minute)})
	require.NoError(t, err)
	assert.JSONEq(t, `{"d":"6h30m0s"}`, string(b))

	for in, want := range map[string]time.Duration{`"6h"`: 6 * time.Hour, `"90m"`: 90 * time.Minute, `"1.5s"`: 1500 * time.Millisecond} {
		var got body
		require.NoError(t, json.Unmarshal([]byte(`{"d":`+in+`}`), &got), in)
		assert.Equal(t, want, got.D.Duration(), in)
	}
	for _, in := range []string{`"6 hours"`, `""`, `21600`, `"SENTINEL"`} {
		var got body
		err := json.Unmarshal([]byte(`{"d":`+in+`}`), &got)
		require.Error(t, err, in)
		assert.NotContains(t, err.Error(), "SENTINEL", "the input is not quoted")
	}
}
