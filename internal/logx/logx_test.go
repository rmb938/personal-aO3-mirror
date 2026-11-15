package logx

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFanoutWritesBoth(t *testing.T) {
	dir := t.TempDir()
	var stderr bytes.Buffer
	l, c, err := New(Options{Level: "info", File: filepath.Join(dir, "x.log"), MaxBytes: 1 << 20, MaxFiles: 2, Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	l.Info("hello", "work_id", 42)
	l.Debug("hidden")
	c.Close()
	raw, _ := os.ReadFile(filepath.Join(dir, "x.log"))
	if !strings.Contains(string(raw), `"work_id":42`) || !strings.Contains(stderr.String(), "work_id=42") {
		t.Fatalf("file=%q stderr=%q", raw, stderr.String())
	}
	if strings.Contains(stderr.String(), "hidden") {
		t.Fatal("debug leaked at info level")
	}
}

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "r.log")
	r, err := OpenRotating(p, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		r.Write([]byte("0123456789"))
	}
	r.Close()
	for _, name := range []string{"r.log", "r.log.1", "r.log.2"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "r.log.3")); err == nil {
		t.Error("kept too many files")
	}
}
