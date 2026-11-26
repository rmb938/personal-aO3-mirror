// Package store owns meta.db (metadata, sources, jobs, request log, FTS) and content.db (raw
// pages and parsed bodies), and separately users.db (the API's accounts, users.go). Pure Go
// SQLite (modernc.org/sqlite), WAL mode, embedded migrations.
//
// Logging here is content-free: only IDs, counts, versions and timings.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"
)

const (
	MetaFile    = "meta.db"
	ContentFile = "content.db"
)

// SlowQuery is the threshold above which store operations are logged at warn.
var SlowQuery = 200 * time.Millisecond

// Store holds both databases. It has no query methods: all access goes through InTx / InReadTx
// (tx.go), so every store call runs inside a transaction.
type Store struct {
	*core
	meta    *sql.DB
	content *sql.DB
}

// core is what the Store and its transactions share: logging, the clock and the zstd coders.
type core struct {
	log *slog.Logger
	now func() time.Time
	enc *zstd.Encoder
	dec *zstd.Decoder
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
	meta, err := openDB(ctx, filepath.Join(dir, MetaFile), dbOptions{})
	if err != nil {
		return nil, err
	}
	content, err := openDB(ctx, filepath.Join(dir, ContentFile), dbOptions{pageSize: ContentPageSize})
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
	s := &Store{core: &core{log: log, now: o.Now, enc: enc, dec: dec}, meta: meta, content: content}
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

// dbOptions configure openDB. The zero value is WAL mode with SQLite's default page size.
type dbOptions struct {
	// pageSize > 0 sets the page size when the file is created; an existing file keeps its page
	// size (changing it needs a VACUUM outside WAL mode).
	pageSize int
	// rollback selects journal_mode=DELETE with synchronous=FULL instead of WAL with
	// synchronous=NORMAL: for a small database with few writes (keys.db).
	rollback bool
	pragmas  []string // more per-connection pragmas, e.g. "secure_delete(ON)"
}

// openDB opens a database with o's journal mode, page size and pragmas.
func openDB(ctx context.Context, path string, o dbOptions) (*sql.DB, error) {
	if o.pageSize > 0 {
		if err := initPageSize(ctx, path, o.pageSize); err != nil {
			return nil, fmt.Errorf("store: init %s: %w", filepath.Base(path), err)
		}
	}
	journal := []string{"journal_mode(WAL)", "synchronous(NORMAL)"}
	if o.rollback {
		journal = []string{"journal_mode(DELETE)", "synchronous(FULL)"}
	}
	q := url.Values{}
	for _, p := range append(append([]string{"busy_timeout(10000)", "foreign_keys(1)"}, journal...), o.pragmas...) {
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

// pageSize returns the page size of the named db ("meta" or "content").
func (s *Store) pageSize(ctx context.Context, name string) (int, error) {
	db := s.meta
	if name == "content" {
		db = s.content
	}
	var n int
	err := db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&n)
	return n, err
}

// Close closes both databases and the zstd coders, returning every error that occurred.
func (s *Store) Close() error {
	var errs []error
	for _, db := range []*sql.DB{s.meta, s.content} {
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

func (c *core) unix() int64 { return c.now().Unix() }

// migrate applies the migrations of database name (migrations.go) in order, each in its own
// transaction.
func (c *core) migrate(ctx context.Context, db *sql.DB, name string) error {
	return c.migrateTo(ctx, db, name, math.MaxInt)
}

// migrateTo is migrate stopping after version upTo (tests build older schemas with it).
func (c *core) migrateTo(ctx context.Context, db *sql.DB, name string, upTo int) error {
	list, ok := migrations[name]
	if !ok {
		return fmt.Errorf("store: no migrations for database %s", name)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("store: %s migrations table: %w", name, err)
	}
	var current int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}
	if err := checkApplied(ctx, db, name, list); err != nil {
		return err
	}
	applied := 0
	for i, m := range list {
		ver := i + 1
		if ver <= current {
			continue
		}
		if ver > upTo {
			break
		}
		start := time.Now()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			return errors.Join(fmt.Errorf("store: %s migration %s: %w", name, m.file, err), tx.Rollback())
		}
		if m.step != nil {
			if err := m.step(ctx, tx); err != nil {
				return errors.Join(fmt.Errorf("store: %s migration %s step: %w", name, m.file, err), tx.Rollback())
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?,?,?)`,
			ver, m.file, c.unix()); err != nil {
			return errors.Join(fmt.Errorf("store: %s record migration %s: %w", name, m.file, err), tx.Rollback())
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		applied++
		c.log.Info("migration applied", "db", name, "version", ver, "file", m.file, "ms", time.Since(start).Milliseconds())
	}
	c.log.Debug("schema current", "db", name, "from", current, "applied", applied)
	return nil
}

// checkApplied refuses a database whose applied migrations this binary does not have (same
// version and file name). That happens when the db was created by a different build, e.g. one
// from before migrations were squashed; silently skipping would leave the schema out of date.
func checkApplied(ctx context.Context, db *sql.DB, name string, list []migration) error {
	known := map[int]string{}
	for i, m := range list {
		known[i+1] = m.file
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

// schemaVersion returns the current migration version of the named db ("meta" or "content").
func (s *Store) schemaVersion(ctx context.Context, name string) (int, error) {
	db := s.meta
	if name == "content" {
		db = s.content
	}
	var v int
	err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&v)
	return v, err
}

// timed logs operations slower than SlowQuery (content-free attrs only).
func (c *core) timed(op string, start time.Time, attrs ...any) {
	if d := time.Since(start); d > SlowQuery {
		c.log.Warn("slow store op", append([]any{"op", op, "ms", d.Milliseconds()}, attrs...)...)
	}
}

func (c *core) compress(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return c.enc.EncodeAll(b, make([]byte, 0, len(b)/4))
}

func (c *core) decompress(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, nil
	}
	return c.dec.DecodeAll(b, nil)
}

// ---- SQL helpers ----

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// unixTime converts a stored unix-seconds column (0 = never) to a time.
func unixTime(s int64) time.Time {
	if s == 0 {
		return time.Time{}
	}
	return time.Unix(s, 0)
}

// zeroUnix converts a time to unix seconds for storage (zero time = 0).
func zeroUnix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// placeholders returns n comma-separated bind markers ("?,?,?") for IN lists.
func placeholders(n int) string {
	b := make([]byte, 0, 2*n)
	for i := range n {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '?')
	}
	return string(b)
}

// likeEscape escapes LIKE wildcards in s, for use with `LIKE ? ESCAPE '\'`.
func likeEscape(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' || s[i] == '_' || s[i] == '\\' {
			b = append(b, '\\')
		}
		b = append(b, s[i])
	}
	return string(b)
}

// queryInt64s runs a single-integer-column query and returns its values in order.
func queryInt64s(ctx context.Context, db querier, q string, args ...any) (out []int64, err error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
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
