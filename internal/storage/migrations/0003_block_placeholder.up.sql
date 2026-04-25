-- 0003: Mark synthetic "placeholder" blocks created when a user assigns a tag
-- to a dragged time range that extends beyond actual tracked focus blocks.
-- Placeholder blocks have no real process / window data and are deleted again
-- once their tag is removed.

ALTER TABLE focus_blocks ADD COLUMN is_placeholder INTEGER NOT NULL DEFAULT 0;
