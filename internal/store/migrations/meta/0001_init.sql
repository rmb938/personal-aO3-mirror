-- meta.db v1: metadata, sources, job queue, request log. Bulk text lives in content.db.

CREATE TABLE works (
    id                INTEGER PRIMARY KEY,          -- AO3 work id
    title             TEXT NOT NULL DEFAULT '',
    summary_html      TEXT NOT NULL DEFAULT '',
    notes_html        TEXT NOT NULL DEFAULT '',
    endnotes_html     TEXT NOT NULL DEFAULT '',
    language          TEXT NOT NULL DEFAULT '',
    words             INTEGER NOT NULL DEFAULT 0,
    chapters_posted   INTEGER NOT NULL DEFAULT 0,
    chapters_expected INTEGER NOT NULL DEFAULT 0,   -- 0 = unknown ("?")
    complete          INTEGER NOT NULL DEFAULT 0,
    published         TEXT NOT NULL DEFAULT '',     -- YYYY-MM-DD
    revised_at        TEXT NOT NULL DEFAULT '',     -- YYYY-MM-DD
    kudos             INTEGER NOT NULL DEFAULT 0,
    hits              INTEGER NOT NULL DEFAULT 0,
    comments          INTEGER NOT NULL DEFAULT 0,
    bookmarks         INTEGER NOT NULL DEFAULT 0,
    restricted        INTEGER NOT NULL DEFAULT 0,
    anonymous         INTEGER NOT NULL DEFAULT 0,
    status            TEXT NOT NULL DEFAULT 'ok'
                      CHECK (status IN ('ok','deleted','hidden','restricted','unrevealed')),
    status_changed_at INTEGER NOT NULL DEFAULT 0,
    first_seen        INTEGER NOT NULL,
    last_listed       INTEGER NOT NULL DEFAULT 0,   -- last seen in any listing blurb
    last_fetched      INTEGER NOT NULL DEFAULT 0,   -- last full page fetch
    last_checked      INTEGER NOT NULL DEFAULT 0,   -- last verification request
    fetched_revised   TEXT NOT NULL DEFAULT '',     -- revised_at/chapters/words at last page fetch,
    fetched_chapters  INTEGER NOT NULL DEFAULT 0,   -- used to decide whether a re-fetch is needed
    fetched_words     INTEGER NOT NULL DEFAULT 0,
    parser_version    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX works_revised ON works (revised_at);
CREATE INDEX works_kudos ON works (kudos);
CREATE INDEX works_hits ON works (hits);
CREATE INDEX works_words ON works (words);
CREATE INDEX works_status ON works (status);

CREATE TABLE chapters (
    work_id    INTEGER NOT NULL REFERENCES works (id) ON DELETE CASCADE,
    position   INTEGER NOT NULL,
    chapter_id INTEGER NOT NULL DEFAULT 0,
    title      TEXT NOT NULL DEFAULT '',
    published  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (work_id, position)
);

CREATE TABLE creators (
    id         INTEGER PRIMARY KEY,
    user_login TEXT NOT NULL,
    pseud      TEXT NOT NULL,
    UNIQUE (user_login, pseud)
);

CREATE TABLE work_creators (
    work_id    INTEGER NOT NULL REFERENCES works (id) ON DELETE CASCADE,
    creator_id INTEGER NOT NULL REFERENCES creators (id),
    position   INTEGER NOT NULL,
    PRIMARY KEY (work_id, creator_id)
);
CREATE INDEX work_creators_creator ON work_creators (creator_id);

CREATE TABLE tags (
    id              INTEGER PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE COLLATE NOCASE,
    type            TEXT NOT NULL DEFAULT 'Unknown',
    canonical       INTEGER,                         -- NULL until tag info fetched
    merger_id       INTEGER REFERENCES tags (id),    -- synonym of
    info_fetched_at INTEGER NOT NULL DEFAULT 0,
    first_seen      INTEGER NOT NULL
);
CREATE INDEX tags_type ON tags (type);

CREATE TABLE tag_parents (                           -- e.g. character -> fandom, fandom -> media
    child_id  INTEGER NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    parent_id INTEGER NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (child_id, parent_id)
);

CREATE TABLE tag_meta (                              -- meta tag relationships
    sub_id  INTEGER NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    meta_id INTEGER NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (sub_id, meta_id)
);

CREATE TABLE work_tags (
    work_id  INTEGER NOT NULL REFERENCES works (id) ON DELETE CASCADE,
    tag_id   INTEGER NOT NULL REFERENCES tags (id),
    position INTEGER NOT NULL,
    PRIMARY KEY (work_id, tag_id)
);
CREATE INDEX work_tags_tag ON work_tags (tag_id);

CREATE TABLE series (
    id           INTEGER PRIMARY KEY,                -- AO3 series id
    title        TEXT NOT NULL DEFAULT '',
    summary_html TEXT NOT NULL DEFAULT '',
    notes_html   TEXT NOT NULL DEFAULT '',
    complete     INTEGER NOT NULL DEFAULT 0,
    words        INTEGER NOT NULL DEFAULT 0,
    works_count  INTEGER NOT NULL DEFAULT 0,
    bookmarks    INTEGER NOT NULL DEFAULT 0,
    begun        TEXT NOT NULL DEFAULT '',
    updated      TEXT NOT NULL DEFAULT '',
    first_seen   INTEGER NOT NULL,
    last_fetched INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE series_works (
    series_id INTEGER NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    work_id   INTEGER NOT NULL REFERENCES works (id) ON DELETE CASCADE,
    position  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (series_id, work_id)
);
CREATE INDEX series_works_work ON series_works (work_id);

CREATE TABLE series_creators (
    series_id  INTEGER NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    creator_id INTEGER NOT NULL REFERENCES creators (id),
    PRIMARY KEY (series_id, creator_id)
);

CREATE TABLE collections (
    id    INTEGER PRIMARY KEY,
    name  TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL DEFAULT ''
);

CREATE TABLE work_collections (
    work_id       INTEGER NOT NULL REFERENCES works (id) ON DELETE CASCADE,
    collection_id INTEGER NOT NULL REFERENCES collections (id),
    PRIMARY KEY (work_id, collection_id)
);

-- AO3-equivalent search: metadata only (AO3 search does not index body text).
CREATE VIRTUAL TABLE works_fts USING fts5 (
    title, summary, creators, tags,
    tokenize = 'unicode61 remove_diacritics 2'
);

CREATE TABLE sources (
    id                INTEGER PRIMARY KEY,
    kind              TEXT NOT NULL
                      CHECK (kind IN ('tag','user','pseud','user_bookmarks','series','collection','work')),
    target            TEXT NOT NULL,                 -- tag name / login / series id / ...
    filters_json      TEXT NOT NULL DEFAULT '{}',
    path              TEXT NOT NULL,                 -- canonical robots-allowed listing path
    state             TEXT NOT NULL DEFAULT 'pending_confirm'
                      CHECK (state IN ('pending_confirm','active','paused','cancelled','done_backfill')),
    backfill_order    TEXT NOT NULL,
    poll_interval_s   INTEGER NOT NULL,
    priority          INTEGER NOT NULL DEFAULT 0,
    total_works       INTEGER NOT NULL DEFAULT -1,  -- -1 = unknown
    est_requests      INTEGER NOT NULL DEFAULT -1,
    backfill_cursor   TEXT NOT NULL DEFAULT '{}',   -- JSON: page, date window
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL,
    last_poll_at      INTEGER NOT NULL DEFAULT 0,
    next_poll_at      INTEGER NOT NULL DEFAULT 0,
    last_full_scan_at INTEGER NOT NULL DEFAULT 0,
    backfill_done_at  INTEGER NOT NULL DEFAULT 0,
    last_error        TEXT NOT NULL DEFAULT '',      -- content-free error code
    UNIQUE (kind, target, filters_json)
);

CREATE TABLE source_works (
    source_id  INTEGER NOT NULL REFERENCES sources (id) ON DELETE CASCADE,
    work_id    INTEGER NOT NULL REFERENCES works (id) ON DELETE CASCADE,
    first_seen INTEGER NOT NULL,
    last_seen  INTEGER NOT NULL,
    PRIMARY KEY (source_id, work_id)
);
CREATE INDEX source_works_work ON source_works (work_id);

CREATE TABLE jobs (
    id             INTEGER PRIMARY KEY,
    kind           TEXT NOT NULL,                   -- listing_page, work, tag_info, verify, image, ...
    source_id      INTEGER REFERENCES sources (id) ON DELETE SET NULL,
    target         TEXT NOT NULL,                   -- path or id; content-free
    priority_class INTEGER NOT NULL,
    state          TEXT NOT NULL DEFAULT 'queued'
                   CHECK (state IN ('queued','running','done','failed','cancelled')),
    not_before     INTEGER NOT NULL DEFAULT 0,
    attempts       INTEGER NOT NULL DEFAULT 0,
    max_attempts   INTEGER NOT NULL DEFAULT 5,
    last_error     TEXT NOT NULL DEFAULT '',
    dedupe_key     TEXT NOT NULL,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    started_at     INTEGER NOT NULL DEFAULT 0,
    finished_at    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX jobs_pick ON jobs (state, priority_class, not_before);
CREATE INDEX jobs_source ON jobs (source_id, state);
CREATE UNIQUE INDEX jobs_dedupe_active ON jobs (dedupe_key) WHERE state IN ('queued','running');

CREATE TABLE job_events (                          -- content-free trace for `ao3mirror debug job`
    id      INTEGER PRIMARY KEY,
    job_id  INTEGER NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    ts      INTEGER NOT NULL,
    event   TEXT NOT NULL,
    detail  TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX job_events_job ON job_events (job_id);

CREATE TABLE requests (                            -- one row per HTTP exchange; content-free
    id          INTEGER PRIMARY KEY,
    ts          INTEGER NOT NULL,
    req_id      TEXT NOT NULL,
    job_id      INTEGER,
    kind        TEXT NOT NULL,
    path        TEXT NOT NULL,                     -- httpx.SafePath (names hashed)
    status      INTEGER NOT NULL,
    class       TEXT NOT NULL,
    reason      TEXT NOT NULL,
    bytes       INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL,
    waited_ms   INTEGER NOT NULL,
    hop         INTEGER NOT NULL
);
CREATE INDEX requests_ts ON requests (ts);

CREATE TABLE images (
    id           INTEGER PRIMARY KEY,
    url          TEXT NOT NULL UNIQUE,
    sha256       TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL DEFAULT '',
    bytes        INTEGER NOT NULL DEFAULT 0,
    status       TEXT NOT NULL DEFAULT 'queued'
                 CHECK (status IN ('queued','ok','failed','skipped','too_large','disallowed')),
    fetched_at   INTEGER NOT NULL DEFAULT 0,
    last_error   TEXT NOT NULL DEFAULT ''
);

CREATE TABLE work_images (
    work_id  INTEGER NOT NULL REFERENCES works (id) ON DELETE CASCADE,
    image_id INTEGER NOT NULL REFERENCES images (id),
    PRIMARY KEY (work_id, image_id)
);
