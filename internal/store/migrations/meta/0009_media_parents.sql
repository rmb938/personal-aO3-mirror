-- Media tags (Part 2 stage 12e). AO3 gives a fandom only media parents (Fandom#parent_types),
-- but tag pages stored every parent as Unknown until it appeared on a work, which a medium never
-- does. store.UpdateTagInfo now types a fandom's parents Media; this types the ones stored before.

UPDATE tags SET type = 'Media'
WHERE type = 'Unknown'
  AND id IN (SELECT r.parent_id FROM tag_parents r JOIN tags c ON c.id = r.child_id WHERE c.type = 'Fandom');
