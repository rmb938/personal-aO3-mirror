package store

// Benchmarks for content.db layout choices (page size, column order), using synthetic random
// blobs (incompressible, like the zstd data we store). No real data is involved.
//
//	go test ./internal/store -run '^$' -bench 'Blob' -benchtime 2000x
//
// Sizes: raw pages average ~46 KB compressed in practice; chapter bodies are smaller, and long
// chapters can be well over 100 KB.

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"net/url"
	"path/filepath"
	"testing"
)

const benchRows = 500

func benchDB(b *testing.B, pageSize int) *sql.DB {
	b.Helper()
	q := url.Values{}
	q.Add("_pragma", "synchronous(NORMAL)")
	db, err := sql.Open("sqlite", "file:"+filepath.Join(b.TempDir(), "bench.db")+"?"+q.Encode())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1) // one connection: the page_size set below is the one that creates the file
	// page_size only applies before the file is initialized, and not once it is in WAL mode, so
	// set it first, then switch to WAL.
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA page_size = %d`, pageSize)); err != nil {
		b.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		b.Fatal(err)
	}
	var got int
	if err := db.QueryRow(`PRAGMA page_size`).Scan(&got); err != nil || got != pageSize {
		b.Fatalf("page_size = %d, want %d (%v)", got, pageSize, err)
	}
	return db
}

func randBlob(r *rand.Rand, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(r.Uint32())
	}
	return out
}

// BenchmarkBlobRead: random single-blob reads (like opening one work), by page and blob size.
func BenchmarkBlobRead(b *testing.B) {
	for _, size := range []int{8 << 10, 46 << 10, 150 << 10} {
		for _, ps := range []int{4096, 8192, 16384} {
			b.Run(fmt.Sprintf("blob=%dKB/page=%d", size>>10, ps), func(b *testing.B) {
				db := benchDB(b, ps)
				if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, meta INTEGER NOT NULL, data BLOB NOT NULL)`); err != nil {
					b.Fatal(err)
				}
				r := rand.New(rand.NewPCG(1, 2))
				tx, _ := db.Begin()
				for i := 1; i <= benchRows; i++ {
					if _, err := tx.Exec(`INSERT INTO t (id, meta, data) VALUES (?,?,?)`, i, i, randBlob(r, size)); err != nil {
						b.Fatal(err)
					}
				}
				if err := tx.Commit(); err != nil {
					b.Fatal(err)
				}
				b.SetBytes(int64(size))
				b.ResetTimer()
				var buf []byte
				for i := 0; i < b.N; i++ {
					if err := db.QueryRowContext(context.Background(), `SELECT data FROM t WHERE id = ?`, 1+r.IntN(benchRows)).Scan(&buf); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkBlobWrite: inserting blobs one transaction each (like storing a fetched work).
func BenchmarkBlobWrite(b *testing.B) {
	const size = 46 << 10
	for _, ps := range []int{4096, 8192, 16384} {
		b.Run(fmt.Sprintf("blob=%dKB/page=%d", size>>10, ps), func(b *testing.B) {
			db := benchDB(b, ps)
			if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, data BLOB NOT NULL)`); err != nil {
				b.Fatal(err)
			}
			r := rand.New(rand.NewPCG(3, 4))
			blob := randBlob(r, size)
			b.SetBytes(size)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := db.Exec(`INSERT INTO t (data) VALUES (?)`, blob); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkChapterColumnOrder: reading only the small columns (summary/notes) of a chapter row,
// with the large body stored first (current schema) or last.
func BenchmarkChapterColumnOrder(b *testing.B) {
	schemas := map[string]string{
		"body_first": `CREATE TABLE c (work_id INTEGER NOT NULL, position INTEGER NOT NULL, body BLOB NOT NULL,
			notes BLOB, endnotes BLOB, summary BLOB, PRIMARY KEY (work_id, position))`,
		"body_last": `CREATE TABLE c (work_id INTEGER NOT NULL, position INTEGER NOT NULL, notes BLOB, endnotes BLOB,
			summary BLOB, body BLOB NOT NULL, PRIMARY KEY (work_id, position))`,
	}
	for _, size := range []int{30 << 10, 150 << 10} {
		for _, order := range []string{"body_first", "body_last"} {
			for _, ps := range []int{4096, 16384} {
				for _, cols := range []string{"summary", "all"} {
					b.Run(fmt.Sprintf("body=%dKB/%s/page=%d/read=%s", size>>10, order, ps, cols), func(b *testing.B) {
						db := benchDB(b, ps)
						if _, err := db.Exec(schemas[order]); err != nil {
							b.Fatal(err)
						}
						r := rand.New(rand.NewPCG(5, 6))
						tx, _ := db.Begin()
						for i := 1; i <= benchRows; i++ {
							if _, err := tx.Exec(`INSERT INTO c (work_id, position, body, notes, endnotes, summary) VALUES (?,1,?,?,?,?)`,
								i, randBlob(r, size), randBlob(r, 300), randBlob(r, 300), randBlob(r, 200)); err != nil {
								b.Fatal(err)
							}
						}
						if err := tx.Commit(); err != nil {
							b.Fatal(err)
						}
						b.ResetTimer()
						var body, notes, end, summary []byte
						for i := 0; i < b.N; i++ {
							id := 1 + r.IntN(benchRows)
							var err error
							if cols == "summary" {
								err = db.QueryRow(`SELECT summary FROM c WHERE work_id = ? AND position = 1`, id).Scan(&summary)
							} else {
								err = db.QueryRow(`SELECT body, notes, endnotes, summary FROM c WHERE work_id = ? AND position = 1`, id).
									Scan(&body, &notes, &end, &summary)
							}
							if err != nil {
								b.Fatal(err)
							}
						}
					})
				}
			}
		}
	}
}
