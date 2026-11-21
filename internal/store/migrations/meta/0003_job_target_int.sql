-- Every job kind targets an integer (page number; work, tag or series id; source add id), so
-- jobs.target becomes INTEGER. SQLite cannot change a column's type: rebuild the table.
--
-- The migration runs in a transaction with foreign keys on, where `PRAGMA foreign_keys` is a
-- no-op. Dropping jobs would then cascade-delete every job_events row, so job_events is rebuilt
-- too: its copy references jobs_new, and renaming jobs_new to jobs rewrites that reference.
--
-- The CHECK makes the copy fail on a target that is not an integer (INTEGER affinity turns
-- the text '12' into 12 and leaves anything else text), and keeps rejecting one later.

CREATE TABLE jobs_new (
    id             INTEGER PRIMARY KEY,
    kind           TEXT NOT NULL,                   -- poll, backfill, work, tag_info, series_info, source_add
    source_id      INTEGER REFERENCES sources (id) ON DELETE SET NULL,
    target         INTEGER NOT NULL CHECK (typeof(target) = 'integer'), -- page number or id
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
INSERT INTO jobs_new (id, kind, source_id, target, priority_class, state, not_before, attempts, max_attempts,
    last_error, dedupe_key, created_at, updated_at, started_at, finished_at)
SELECT id, kind, source_id, target, priority_class, state, not_before, attempts, max_attempts,
    last_error, dedupe_key, created_at, updated_at, started_at, finished_at
FROM jobs;

CREATE TABLE job_events_new (                      -- content-free trace for `ao3mirror debug job`
    id      INTEGER PRIMARY KEY,
    job_id  INTEGER NOT NULL REFERENCES jobs_new (id) ON DELETE CASCADE,
    ts      INTEGER NOT NULL,
    event   TEXT NOT NULL,
    detail  TEXT NOT NULL DEFAULT '{}'
);
-- A start event's target is a copy of its job's (checked above), now stored as a number.
INSERT INTO job_events_new (id, job_id, ts, event, detail)
SELECT id, job_id, ts, event,
    CASE WHEN json_type(detail, '$.target') = 'text'
        THEN json_set(detail, '$.target', CAST(json_extract(detail, '$.target') AS INTEGER))
        ELSE detail END
FROM job_events;

DROP TABLE job_events;
DROP TABLE jobs;
ALTER TABLE jobs_new RENAME TO jobs;
ALTER TABLE job_events_new RENAME TO job_events;

CREATE INDEX jobs_pick ON jobs (state, priority_class, not_before);
CREATE INDEX jobs_source ON jobs (source_id, state);
CREATE UNIQUE INDEX jobs_dedupe_active ON jobs (dedupe_key) WHERE state IN ('queued','running');
CREATE INDEX jobs_finished ON jobs (state, finished_at);
CREATE INDEX jobs_state ON jobs (state);
CREATE INDEX job_events_job ON job_events (job_id);
