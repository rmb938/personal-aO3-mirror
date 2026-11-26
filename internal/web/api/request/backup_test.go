package request

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBackupValid(t *testing.T) {
	for _, n := range []string{"2026-11-24", "b", "nightly_01.bak", "A-z.0_9", strings.Repeat("a", MaxBackupNameBytes)} {
		assert.Empty(t, errorsOf(t, &Backup{Name: n}), n)
	}
}

func TestBackupInvalid(t *testing.T) {
	want := map[string]string{"/name": detailBackupName.Message()}
	for _, n := range []string{"", ".", "..", ".hidden", "a/b", "/abs", "../up", `a\b`, "a b", "a\x00b", "é",
		"\xff", strings.Repeat("a", MaxBackupNameBytes+1)} {
		assert.Equal(t, want, errorsOf(t, &Backup{Name: n}), "%q", n)
	}
}
