package request

import (
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
)

// MaxBackupDirBytes bounds a backup directory path (Linux's PATH_MAX is 4096).
const MaxBackupDirBytes = 1024

var detailBackupDir = problem.NewDetail("must be an absolute, clean path (no trailing slash, '.' or '..' " +
	"segments) other than /, of at most " + strconv.Itoa(MaxBackupDirBytes) + " bytes")

// Backup is the body of POST /api/v1/admin/backup (admin socket).
type Backup struct {
	// Dir is the directory serve creates for the copies (required). It must not exist; its
	// parent must. The CLI makes a relative path absolute before sending it.
	Dir string `json:"dir"`
}

// Validate records a dir that is missing, too long, not valid UTF-8, not absolute or not clean.
func (b *Backup) Validate(v *problem.Validation) {
	d := b.Dir
	if d == "" || len(d) > MaxBackupDirBytes || !utf8.ValidString(d) || strings.ContainsRune(d, 0) ||
		!filepath.IsAbs(d) || filepath.Clean(d) != d || d == "/" {
		v.Member(detailBackupDir, "dir")
	}
}
