// Package store owns meta.db (metadata, sources, jobs, request log, FTS) and content.db (raw
// pages and parsed bodies). Pure Go SQLite (modernc.org/sqlite), WAL mode, embedded migrations.
//
// Logging here is content-free: only IDs, counts, versions and timings.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
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
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}
	log := o.Logger.With("component", "store")
	meta, err := openDB(ctx, filepath.Join(dir, MetaFile), 0)
	if err != nil {
		return nil, err
	}
	content, err := openDB(ctx, filepath.Join(dir, ContentFile), ContentPageSize)
	if err != nil {
		return nil, errors.Join(err, meta.Close())
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
	if err != nil {
		return nil, errors.Join(fmt.Errorf("store: zstd encoder: %w", err), meta.Close(), content.Close())
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("store: zstd decoder: %w", err), enc.Close(), meta.Close(), content.Close())
	}
	s := &Store{Meta: meta, Content: content, log: log, now: o.Now, enc: enc, dec: dec}
	if err := s.migrate(ctx, meta, "meta"); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	if err := s.migrate(ctx, content, "content"); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	return s, nil
}

// ContentPageSize is the page size for new content.db files. content.db holds zstd blobs (raw
// pages ~46 KB, chapter bodies); larger pages read them a little faster (see
// blob_bench_test.go). meta.db keeps SQLite's default: small rows, many index lookups.
const ContentPageSize = 16384

// openDB opens a database. pageSize > 0 sets the page size when the file is created; an
// existing file keeps its page size (changing it needs a VACUUM outside WAL mode).
func openDB(ctx context.Context, path string, pageSize int) (*sql.DB, error) {
	if pageSize > 0 {
		if err := initPageSize(ctx, path, pageSize); err != nil {
			return nil, fmt.Errorf("store: init %s: %w", filepath.Base(path), err)
		}
	}
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
	if err := db.PingContext(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("store: open %s: %w", filepath.Base(path), err), db.Close())
	}
	return db, nil
}

// initPageSize creates a new database file with the given page size. It must run before
// anything else touches the file: page_size only applies before the file is initialized and
// cannot change once it is in WAL mode (a page_size _pragma in the DSN is ignored for that reason).
func initPageSize(ctx context.Context, path string, pageSize int) (err error) {
	if _, err := os.Stat(path); err == nil {
		return nil // existing file: keep its page size
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`PRAGMA page_size = %d`, pageSize)); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `PRAGMA journal_mode = WAL`) // writes the header: the file now has pageSize
	return err
}

// PageSize returns the page size of the named db ("meta" or "content").
func (s *Store) PageSize(ctx context.Context, name string) (int, error) {
	db := s.Meta
	if name == "content" {
		db = s.Content
	}
	var n int
	err := db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&n)
	return n, err
}

// Close closes both databases and the zstd coders, returning every error that occurred.
func (s *Store) Close() error {
	var errs []error
	for _, db := range []*sql.DB{s.Meta, s.Content} {
		if db != nil {
			errs = append(errs, db.Close())
		}
	}
	if s.enc != nil {
		errs = append(errs, s.enc.Close())
	}
	if s.dec != nil {
		s.dec.Close() // returns nothing
	}
	return errors.Join(errs...)
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
	if err := checkApplied(ctx, db, name, entries); err != nil {
		return err
	}
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
			return errors.Join(fmt.Errorf("store: %s migration %s: %w", name, e.Name(), err), tx.Rollback())
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?,?,?)`,
			ver, e.Name(), s.unix()); err != nil {
			return errors.Join(fmt.Errorf("store: %s record migration %s: %w", name, e.Name(), err), tx.Rollback())
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

// checkApplied refuses a database whose applied migrations this binary does not have (same
// version and file name). That happens when the db was created by a different build, e.g. one
// from before migrations were squashed; silently skipping would leave the schema out of date.
func checkApplied(ctx context.Context, db *sql.DB, name string, entries []fs.DirEntry) error {
	known := map[int]string{}
	for _, e := range entries {
		if ver, err := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0]); err == nil {
			known[ver] = e.Name()
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT version, name FROM schema_migrations ORDER BY version`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ver int
		var file string
		if err := rows.Scan(&ver, &file); err != nil {
			return err
		}
		if known[ver] != file {
			return fmt.Errorf("store: %s database has migration %s, which this build does not know "+
				"(created by a different build?); use the matching binary or recreate the store", name, file)
		}
	}
	return rows.Err()
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

// withinOneDay reports whether two YYYY-MM-DD dates are equal or one day apart. Anything that
// does not parse is not within a day.
func withinOneDay(a, b string) bool {
	ta, errA := time.Parse(time.DateOnly, a)
	tb, errB := time.Parse(time.DateOnly, b)
	if errA != nil || errB != nil {
		return false
	}
	d := ta.Sub(tb)
	return d >= -24*time.Hour && d <= 24*time.Hour
}
