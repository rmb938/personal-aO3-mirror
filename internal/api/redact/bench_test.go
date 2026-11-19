package redact

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// benchBody is a synthetic list page: 100 works with 12 tags each.
func benchBody(b *testing.B) []byte {
	b.Helper()
	words := int64(54321)
	l := testList{Page: 1, PerPage: 100, Total: 5000}
	for i := range 100 {
		w := testWork{
			ID: int64(i + 1), Title: fmt.Sprintf("Fixture Title %d", i), State: "done",
			Complete: i%2 == 0, Words: &words, UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			PublishedOn: "2025-12-31", Kinds: []testKind{"a", "b"},
			Notes: []string{strings.Repeat("lorem ipsum dolor sit amet ", 40)},
		}
		for j := range 12 {
			w.Tags = append(w.Tags, testTag{ID: int64(j), Name: fmt.Sprintf("Fixture Tag %d", j), Type: "fandom"})
		}
		l.Items = append(l.Items, w)
	}
	body, err := json.Marshal(l)
	require.NoError(b, err)
	return body
}

func BenchmarkEnforce(b *testing.B) {
	e, _ := newTestEnforcer(b)
	body := benchBody(b)
	for _, mode := range []Mode{Hashed, Revealed} {
		b.Run(mode.String(), func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := e.Enforce(mode, listType, body); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkHash(b *testing.B) {
	h, err := NewHasher(0, nil)
	require.NoError(b, err)
	m := h.Current().newMac()
	b.ReportAllocs()
	for b.Loop() {
		_ = m.hash("Fixture Tag Name")
	}
}
