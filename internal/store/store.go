// Package store owns meta.db (metadata, sources, jobs, request log, FTS) and content.db (raw
// pages and parsed bodies). Pure Go SQLite (modernc.org/sqlite), WAL mode, embedded migrations.
//
// Logging here is content-free: only IDs, counts, versions and timings.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"
)

//go:embed migrations
var migrationsFS embed.FS

const (
	MetaFile    = "meta.db"
	ContentFile = "content.db"
)

// SlowQuery is the threshold above which store operations are logged at warn.
var SlowQuery = 200 * time.Millisecond

type Store struct {
	Meta    *sql.DB
	Content *sql.DB
	log     *slog.Logger
	now     func() time.Time
	enc     *zstd.Encoder
	dec     *zstd.Decoder
}

// Options configures Open.
type Options struct {
	Logger *slog.Logger
	Now    func() time.Time // for tests
}

// Open opens (creating if needed) both databases under dir and applies migrations.
func Open(ctx context.Context, dir string, o Options) (*Store, error) {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}
	log := o.Logger.With("component", "store")
	meta, err := openDB(filepath.Join(dir, MetaFile))
	if err != nil {
		return nil, err
	}
	content, err := openDB(filepath.Join(dir, ContentFile))
	if err != nil {
		meta.Close()
		return nil, err
	}
	enc, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
	dec, _ := zstd.NewReader(nil)
	s := &Store{Meta: meta, Content: content, log: log, now: o.Now, enc: enc, dec: dec}
	if err := s.migrate(ctx, meta, "meta"); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.migrate(ctx, content, "content"); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func openDB(path string) (*sql.DB, error) {
	q := url.Values{}
	for _, p := range []string{"busy_timeout(10000)", "journal_mode(WAL)", "foreign_keys(1)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate") // take the write lock at BEGIN: avoids SQLITE_BUSY on upgrade
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", filepath.Base(path), err)
	}
	db.SetMaxOpenConns(4)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: open %s: %w", filepath.Base(path), err)
	}
	return db, nil
}

func (s *Store) Close() error {
	var first error
	for _, db := range []*sql.DB{s.Meta, s.Content} {
		if db != nil {
			if err := db.Close(); err != nil && first == nil {
				first = err
			}
		}
	}
	if s.enc != nil {
		s.enc.Close()
	}
	if s.dec != nil {
		s.dec.Close()
	}
	return first
}

func (s *Store) unix() int64 { return s.now().Unix() }

// migrate applies migrations/<name>/NNNN_*.sql in order, each in its own transaction.
func (s *Store) migrate(ctx context.Context, db *sql.DB, name string) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("store: %s migrations table: %w", name, err)
	}
	var current int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}
	dir := "migrations/" + name
	entries, err := fs.ReadDir(migrationsFS, dir)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	applied := 0
	for _, e := range entries {
		ver, err := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("store: bad migration name %s", e.Name())
		}
		if ver <= current {
			continue
		}
		body, err := migrationsFS.ReadFile(dir + "/" + e.Name())
		if err != nil {
			return err
		}
		start := time.Now()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: %s migration %s: %w", name, e.Name(), err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?,?,?)`,
			ver, e.Name(), s.unix()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		applied++
		s.log.Info("migration applied", "db", name, "version", ver, "file", e.Name(), "ms", time.Since(start).Milliseconds())
	}
	s.log.Debug("schema current", "db", name, "from", current, "applied", applied)
	return nil
}

// SchemaVersion returns the current migration version of the named db ("meta" or "content").
func (s *Store) SchemaVersion(ctx context.Context, name string) (int, error) {
	db := s.Meta
	if name == "content" {
		db = s.Content
	}
	var v int
	err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&v)
	return v, err
}

// timed logs operations slower than SlowQuery (content-free attrs only).
func (s *Store) timed(op string, start time.Time, attrs ...any) {
	if d := time.Since(start); d > SlowQuery {
		s.log.Warn("slow store op", append([]any{"op", op, "ms", d.Milliseconds()}, attrs...)...)
	}
}

func (s *Store) compress(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return s.enc.EncodeAll(b, make([]byte, 0, len(b)/4))
}

func (s *Store) decompress(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, nil
	}
	return s.dec.DecodeAll(b, nil)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
