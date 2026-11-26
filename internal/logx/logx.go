// Package logx sets up structured logging: JSON to a rotating file under data_dir, text to stderr.
//
// Logs must stay content-free (see CLAUDE.md): log numeric IDs, statuses, sizes, timings and
// error codes, never titles, names, tag names or text from AO3 pages.
package logx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// Options configures New.
type Options struct {
	Level string // debug|info|warn|error
	// Dir is the directory File is in (the data directory, opened as a root); File is relative
	// to it (log.file). An empty File disables file logging.
	Dir      *os.Root
	File     string
	MaxBytes int64
	MaxFiles int
	Stderr   io.Writer // defaults to os.Stderr; nil-able for tests via io.Discard
}

// New returns a logger and a closer for the log file.
func New(o Options) (*slog.Logger, io.Closer, error) {
	lvl, err := ParseLevel(o.Level)
	if err != nil {
		return nil, nil, err
	}
	hopts := &slog.HandlerOptions{Level: lvl, AddSource: lvl == slog.LevelDebug}
	stderr := o.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	handlers := []slog.Handler{slog.NewTextHandler(stderr, hopts)}
	var closer io.Closer = nopCloser{}
	if o.File != "" {
		if o.Dir == nil {
			return nil, nil, errors.New("logx: a log file needs its directory")
		}
		rf, err := OpenRotating(o.Dir, o.File, o.MaxBytes, o.MaxFiles)
		if err != nil {
			return nil, nil, err
		}
		closer = rf
		handlers = append(handlers, slog.NewJSONHandler(rf, hopts))
	}
	return slog.New(fanout(handlers)), closer, nil
}

func ParseLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q", s)
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// fanout sends each record to every handler.
type fanout []slog.Handler

func (f fanout) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f fanout) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f {
		if h.Enabled(ctx, r.Level) {
			errs = append(errs, h.Handle(ctx, r.Clone()))
		}
	}
	return errors.Join(errs...)
}

func (f fanout) WithAttrs(as []slog.Attr) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(as)
	}
	return out
}

func (f fanout) WithGroup(name string) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(name)
	}
	return out
}

// RotatingFile is a size-based rotating log file: name, name.1 ... name.N, all in dir.
type RotatingFile struct {
	mu       sync.Mutex
	dir      *os.Root
	path     string // relative to dir
	maxBytes int64
	maxFiles int
	f        *os.File
	size     int64
}

// OpenRotating opens (creating it and its directories when missing) the log file name in dir.
func OpenRotating(dir *os.Root, name string, maxBytes int64, maxFiles int) (*RotatingFile, error) {
	if err := dir.MkdirAll(filepath.Dir(name), 0o750); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	r := &RotatingFile{dir: dir, path: name, maxBytes: maxBytes, maxFiles: maxFiles}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RotatingFile) open() error {
	f, err := r.dir.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.maxBytes > 0 && r.size+int64(len(p)) > r.maxBytes && r.size > 0 {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *RotatingFile) rotate() error {
	r.f.Close()
	for i := r.maxFiles - 1; i >= 1; i-- {
		_ = r.dir.Rename(fmt.Sprintf("%s.%d", r.path, i), fmt.Sprintf("%s.%d", r.path, i+1))
	}
	_ = r.dir.Remove(fmt.Sprintf("%s.%d", r.path, r.maxFiles+1))
	if err := r.dir.Rename(r.path, r.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return r.open()
}

func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Close()
}

type ctxKey struct{}

// With returns ctx carrying a logger enriched with attrs (e.g. "job_id", 12, "source_id", 3).
func With(ctx context.Context, l *slog.Logger, args ...any) context.Context {
	return context.WithValue(ctx, ctxKey{}, l.With(args...))
}

// From returns the logger stored in ctx, or fallback (or slog.Default()) if none.
func From(ctx context.Context, fallback *slog.Logger) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	if fallback != nil {
		return fallback
	}
	return slog.Default()
}
