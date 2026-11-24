package request

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBackupValid(t *testing.T) {
	for _, d := range []string{"/srv/backups/2026-11-24", "/b", "/" + strings.Repeat("a", MaxBackupDirBytes-1)} {
		assert.Empty(t, errorsOf(t, &Backup{Dir: d}), d)
	}
}

func TestBackupInvalid(t *testing.T) {
	want := map[string]string{"/dir": detailBackupDir.Message()}
	for _, d := range []string{"", "/", "relative/dir", "./dir", "/srv/backups/", "/srv/../etc", "/srv/./b",
		"//srv", "/srv\x00b", "/srv/\xff", "/" + strings.Repeat("a", MaxBackupDirBytes)} {
		assert.Equal(t, want, errorsOf(t, &Backup{Dir: d}), "%q", d)
	}
}
