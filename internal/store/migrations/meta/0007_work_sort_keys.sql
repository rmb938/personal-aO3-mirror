-- Title and creator sorts (Part 2 stage 9d), after AO3's title_to_sort_on (Work#sorted_title) and
-- authors_to_sort_on. Both are rewritten in Go (store/sortkeys.go), so they are stored: the store
-- recomputes them whenever an upsert rewrites a work's title, anonymity or creators. Each has an
-- index, so a page walks it like the other sorts (the rowid breaks ties).
--
-- The backfill is a Go step (store.backfillSortKeys, run in this migration's transaction).

ALTER TABLE works ADD COLUMN title_sort TEXT NOT NULL DEFAULT '';
ALTER TABLE works ADD COLUMN creator_sort TEXT NOT NULL DEFAULT '';

CREATE INDEX works_title_sort ON works (title_sort);
CREATE INDEX works_creator_sort ON works (creator_sort);
