package redact

import (
	"encoding/json/v2"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testDuration is a Duration type as a DTO package would declare it.
type testDuration time.Duration

func (d testDuration) Duration() time.Duration { return time.Duration(d) }

func (d testDuration) MarshalText() ([]byte, error) { return []byte(time.Duration(d).String()), nil }

type durBody struct {
	Every testDuration   `json:"every"`
	Maybe *testDuration  `json:"maybe,omitzero"`
	List  []testDuration `json:"list"`
}

func TestDurationField(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.RegisterTypes(durBody{}))
	h, err := NewHasher(0, nil)
	require.NoError(t, err)
	e, err := NewEnforcer(reg, h)
	require.NoError(t, err)

	d := testDuration(90 * time.Minute)
	body, err := json.Marshal(durBody{Every: testDuration(6 * time.Hour), Maybe: &d, List: []testDuration{1}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"every":"6h0m0s","maybe":"1h30m0s","list":["1ns"]}`, string(body))
	res, err := e.Enforce(Hashed, reflect.TypeFor[durBody](), body)
	require.NoError(t, err)
	assert.JSONEq(t, string(body), string(res.Body), "durations pass unhashed")

	for _, s := range []string{"6h", "SENTINEL-x", "1h0m0s ", "PT1H", "", "99999999999999999999h"} {
		res, err := e.Enforce(Hashed, reflect.TypeFor[durBody](), []byte(`{"every":"`+s+`"}`))
		require.NoError(t, err)
		assert.NotContains(t, string(res.Body), `"`+s+`"`, "%q is not what Duration.String writes: hashed", s)
	}
	_, err = e.Enforce(Hashed, reflect.TypeFor[durBody](), []byte(`{"every":5}`))
	require.Error(t, err, "a number where a duration string belongs is a schema error")
}

func TestIsDuration(t *testing.T) {
	for _, s := range []string{"0s", "1ns", "1.5s", "6h0m0s", "-1m0s", "2562047h47m16.854775807s"} {
		assert.True(t, isDuration(s), s)
	}
	for _, s := range []string{"", "6h", "1m", "0", "1h0m0s0s", "x", "1d", "+1s"} {
		assert.False(t, isDuration(s), s)
	}
}

type strDuration string

func (strDuration) Duration() time.Duration      { return 0 }
func (strDuration) MarshalText() ([]byte, error) { return nil, nil }

func TestDurationFieldRejects(t *testing.T) {
	type plain struct {
		D time.Duration `json:"d"`
	}
	type notInt struct {
		D strDuration `json:"d"`
	}
	for name, v := range map[string]any{"time.Duration": plain{}, "string kind": notInt{}} {
		assert.Error(t, NewRegistry().RegisterTypes(v), name)
	}
}
