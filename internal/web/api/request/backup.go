package request

import (
	"strconv"

	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
)

// MaxBackupNameBytes bounds a backup name; it is controllers.MaxBackupNameBytes (this package
// imports only the standard library, so a test keeps the two equal).
const MaxBackupNameBytes = 128

var detailBackupName = problem.NewDetail("must be 1 to " + strconv.Itoa(MaxBackupNameBytes) +
	" bytes of letters, digits, '.', '_' and '-', not starting with '.'")

// Backup is the body of POST /api/v1/admin/backup (admin socket).
type Backup struct {
	// Name is the new backup's directory under api.backup_dir (required): one plain path
	// component that must not exist yet.
	Name string `json:"name"`
}

// Validate records a name that is not 1..MaxBackupNameBytes bytes of [A-Za-z0-9._-] or that
// starts with '.' (the rule of controllers.ValidBackupName, which the controller checks again).
func (b *Backup) Validate(v *problem.Validation) {
	if !validBackupName(b.Name) {
		v.Member(detailBackupName, "name")
	}
}

func validBackupName(s string) bool {
	if s == "" || len(s) > MaxBackupNameBytes || s[0] == '.' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
