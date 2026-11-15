-- meta schema v3: tag-info scheduling and the image fetch queue.

CREATE INDEX tags_info ON tags (type, info_fetched_at);

ALTER TABLE images ADD COLUMN host TEXT NOT NULL DEFAULT '';
ALTER TABLE images ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE images ADD COLUMN not_before INTEGER NOT NULL DEFAULT 0;
ALTER TABLE images ADD COLUMN first_seen INTEGER NOT NULL DEFAULT 0;
CREATE INDEX images_queue ON images (status, not_before);
