-- content.db v1: raw pages (source of truth) and parsed bodies. All blobs are zstd.
-- (Pre-release: earlier migrations were folded into this file.)

CREATE TABLE raw_pages (
    id             INTEGER PRIMARY KEY,
    work_id        INTEGER NOT NULL,
    fetched_at     INTEGER NOT NULL,
    path           TEXT NOT NULL,          -- request path (numeric ids only for works)
    sha256         TEXT NOT NULL,          -- of uncompressed HTML
    size           INTEGER NOT NULL,       -- uncompressed bytes
    html_zstd      BLOB NOT NULL,
    UNIQUE (work_id, sha256)
);
CREATE INDEX raw_pages_work ON raw_pages (work_id, fetched_at);

CREATE TABLE chapter_bodies (
    work_id        INTEGER NOT NULL,
    position       INTEGER NOT NULL,
    chapter_id     INTEGER NOT NULL DEFAULT 0,
    body_zstd      BLOB NOT NULL,
    notes_zstd     BLOB,
    endnotes_zstd  BLOB,
    summary_zstd   BLOB,                   -- per-chapter summary (chapters/_chapter.html.erb div#summary)
    PRIMARY KEY (work_id, position)
);

CREATE TABLE work_skins (
    work_id INTEGER PRIMARY KEY,
    css     TEXT NOT NULL
);
