-- Collection works (Part 2 stage 13e). A collection's works filter and its works count read the
-- works of one collection: (collection_id, work_id) covers that read, so it replaces the
-- collection_id-only index of migration 0004 (whose entries hold the rowid, not the work id).

CREATE INDEX work_collections_collection_work ON work_collections (collection_id, work_id);
DROP INDEX work_collections_collection;
