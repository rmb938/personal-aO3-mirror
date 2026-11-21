-- Creator listing (Part 2 stage 13c). A creator's works filter and its works count read the
-- works of one creator: (creator_id, work_id) covers that read, so it replaces the
-- creator_id-only index. The name sort walks creators by pseud, then login, ignoring case.

CREATE INDEX work_creators_creator_work ON work_creators (creator_id, work_id);
DROP INDEX work_creators_creator;

CREATE INDEX creators_name ON creators (pseud COLLATE NOCASE, user_login COLLATE NOCASE);
