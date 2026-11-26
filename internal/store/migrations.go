package store

import (
	"context"
	_ "embed" // the migration SQL, one string per file
)

// migration is one schema step of a database: its file under migrations/<db>/ (NNNN_name.sql,
// NNNN its version: its 1-based position in the list), the file's SQL compiled into the binary,
// and an optional Go step for a data change too costly or too intricate in SQL. The step runs
// after the SQL, in the same transaction.
//
// The SQL is a string fixed at build time, never read at run time, so what migrate executes is
// only ever this list. TestMigrationsListFiles keeps the list in step with the directory.
type migration struct {
	file string
	sql  string
	step func(ctx context.Context, tx querier) error
}

var (
	//go:embed migrations/meta/0001_init.sql
	meta0001 string
	//go:embed migrations/meta/0002_jobs_state.sql
	meta0002 string
	//go:embed migrations/meta/0003_job_target_int.sql
	meta0003 string
	//go:embed migrations/meta/0004_work_listing.sql
	meta0004 string
	//go:embed migrations/meta/0005_work_tag_filters.sql
	meta0005 string
	//go:embed migrations/meta/0006_work_crossover.sql
	meta0006 string
	//go:embed migrations/meta/0007_work_sort_keys.sql
	meta0007 string
	//go:embed migrations/meta/0008_works_fts_series.sql
	meta0008 string
	//go:embed migrations/meta/0009_media_parents.sql
	meta0009 string
	//go:embed migrations/meta/0010_creator_listing.sql
	meta0010 string
	//go:embed migrations/meta/0011_collection_works.sql
	meta0011 string
	//go:embed migrations/meta/0012_image_sha.sql
	meta0012 string

	//go:embed migrations/content/0001_init.sql
	content0001 string

	//go:embed migrations/keys/0001_init.sql
	keys0001 string

	//go:embed migrations/users/0001_init.sql
	users0001 string
	//go:embed migrations/users/0002_signing_key_enc.sql
	users0002 string
	//go:embed migrations/users/0003_recovery_codes_enc.sql
	users0003 string
)

// migrations are each database's migrations, in version order.
var migrations = map[string][]migration{
	"meta": {
		{file: "0001_init.sql", sql: meta0001},
		{file: "0002_jobs_state.sql", sql: meta0002},
		{file: "0003_job_target_int.sql", sql: meta0003},
		{file: "0004_work_listing.sql", sql: meta0004},
		{file: "0005_work_tag_filters.sql", sql: meta0005},
		{file: "0006_work_crossover.sql", sql: meta0006, step: backfillCrossover},
		{file: "0007_work_sort_keys.sql", sql: meta0007, step: backfillSortKeys},
		{file: "0008_works_fts_series.sql", sql: meta0008, step: backfillFTS},
		{file: "0009_media_parents.sql", sql: meta0009},
		{file: "0010_creator_listing.sql", sql: meta0010},
		{file: "0011_collection_works.sql", sql: meta0011},
		{file: "0012_image_sha.sql", sql: meta0012},
	},
	"content": {
		{file: "0001_init.sql", sql: content0001},
	},
	"keys": {
		{file: "0001_init.sql", sql: keys0001},
	},
	"users": {
		{file: "0001_init.sql", sql: users0001},
		{file: "0002_signing_key_enc.sql", sql: users0002},
		{file: "0003_recovery_codes_enc.sql", sql: users0003},
	},
}
