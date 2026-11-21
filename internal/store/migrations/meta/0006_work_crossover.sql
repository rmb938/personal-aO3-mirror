-- Work crossover (Part 2 stage 9c), after AO3's lib/fandom_crossover.rb. Working it out takes a
-- walk of each fandom's meta tags, too costly per query, so it is stored: the store recomputes it
-- on work upsert and when tag info changes a fandom's merger, meta tags or synonyms. No index:
-- like complete, a page walks its sort index and checks it per row.
--
-- The backfill is a Go step (store.backfillCrossover, run in this migration's transaction).

ALTER TABLE works ADD COLUMN crossover INTEGER NOT NULL DEFAULT 0;
