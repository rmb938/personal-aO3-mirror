-- Image reads by hash (Part 2 stage 14). GET /images/{sha256} looks a downloaded image up by
-- its sha256; only ok rows have one, so the index is partial on that status.

CREATE INDEX images_sha256 ON images (sha256) WHERE status = 'ok';
