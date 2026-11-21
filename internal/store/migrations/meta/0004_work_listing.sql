-- Work listing (Part 2 stage 9): an index for every sort column that had none, so a page walks
-- its sort index (the rowid in each index breaks ties), and reverse indexes for the tag, series
-- and collection work listings (stages 12–13).

CREATE INDEX works_published ON works (published);
CREATE INDEX works_comments ON works (comments);
CREATE INDEX works_bookmarks ON works (bookmarks);

CREATE INDEX tag_parents_parent ON tag_parents (parent_id);
CREATE INDEX work_collections_collection ON work_collections (collection_id);
CREATE INDEX series_creators_creator ON series_creators (creator_id);
