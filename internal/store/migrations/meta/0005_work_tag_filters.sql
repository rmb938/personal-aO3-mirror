-- Work tag filters (Part 2 stage 9b). A tag filter expands a tag id to its sub tags (tag_meta,
-- walked from meta to sub) and their synonyms (tags.merger_id), then reads the works tagged with
-- any of them: (tag_id, work_id) covers that read, so it replaces the tag_id-only index.

CREATE INDEX tag_meta_meta ON tag_meta (meta_id);
CREATE INDEX tags_merger ON tags (merger_id) WHERE merger_id IS NOT NULL;

CREATE INDEX work_tags_tag_work ON work_tags (tag_id, work_id);
DROP INDEX work_tags_tag;
