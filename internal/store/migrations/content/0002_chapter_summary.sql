-- content schema v2: per-chapter summaries (chapters/_chapter.html.erb div#summary).
ALTER TABLE chapter_bodies ADD COLUMN summary_zstd BLOB;
