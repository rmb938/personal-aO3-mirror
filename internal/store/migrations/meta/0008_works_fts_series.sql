-- Search series titles too (Part 2 stage 11), as AO3 does (series.title in general_query's
-- fields). FTS5 tables cannot add a column, so works_fts is rebuilt with a series column after
-- the others (store/fts.go). Rows are written in Go from the meta tables (store.updateFTS); the
-- backfill is a Go step (store.backfillFTS, run in this migration's transaction).

DROP TABLE works_fts;

CREATE VIRTUAL TABLE works_fts USING fts5 (
    title, summary, creators, tags, series,
    tokenize = 'unicode61 remove_diacritics 2'
);
