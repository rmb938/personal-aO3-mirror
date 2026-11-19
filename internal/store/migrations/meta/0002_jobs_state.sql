-- GET /api/v1/jobs pages by id within a state. jobs_finished (state, finished_at) matches the
-- state but not the id order, so SQLite sorted every job in that state for each page.
CREATE INDEX jobs_state ON jobs (state);
