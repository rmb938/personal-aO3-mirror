-- meta schema v2: scheduler fairness + daemon heartbeat for `status`.

-- Round-robin across sources within a priority class: least recently served first.
ALTER TABLE sources ADD COLUMN last_served_ms INTEGER NOT NULL DEFAULT 0;

-- Key/value state the daemon publishes (heartbeat, pacer/backoff state). Content-free.
CREATE TABLE daemon_state (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX jobs_finished ON jobs (state, finished_at);
