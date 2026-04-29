-- 0004: Split focus_blocks into two independent tables.
--
-- Until now a single focus_blocks row carried both a process-tracking event
-- (window/process metadata, raw start/end from the polling loop) AND its
-- tagging state (tag_id, description, sync metadata). The new model treats
-- them as separate concepts:
--
--   process_tracks  raw window-focus events from the tracker. No tagging,
--                   no granularity snapping — these reflect what the user
--                   actually had on screen, second by second.
--
--   tag_blocks      tagging spans (manual or auto). Snapped to the configured
--                   granularity and independent of process activity. A tag
--                   block can absorb several process tracks or extend past
--                   them; it is never split by the tracker.
--
-- Existing data is migrated: every non-placeholder focus_block becomes a
-- process_tracks row; every focus_block carrying a tag becomes a tag_blocks
-- row. Adjacent same-tag blocks are NOT consolidated here — the orchestrator
-- going forward emits properly merged spans, and the user can clean up old
-- micro-blocks via the UI if desired.

CREATE TABLE process_tracks (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    process_name TEXT NOT NULL,
    process_path TEXT,
    window_title TEXT NOT NULL,
    start_time   DATETIME NOT NULL,
    end_time     DATETIME,
    duration_sec INTEGER,
    is_idle      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_process_tracks_start ON process_tracks(start_time);
CREATE INDEX idx_process_tracks_open  ON process_tracks(end_time) WHERE end_time IS NULL;

CREATE TABLE tag_blocks (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    tag_id       INTEGER NOT NULL,
    description  TEXT,
    start_time   DATETIME NOT NULL,
    end_time     DATETIME,
    duration_sec INTEGER,
    is_manual    INTEGER NOT NULL DEFAULT 0,
    personio_id  TEXT,
    synced_at    DATETIME,
    FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
);
CREATE INDEX idx_tag_blocks_start ON tag_blocks(start_time);
CREATE INDEX idx_tag_blocks_tag   ON tag_blocks(tag_id);
CREATE INDEX idx_tag_blocks_open  ON tag_blocks(end_time) WHERE end_time IS NULL;

-- Move every non-placeholder focus_block into process_tracks. Placeholder
-- rows had no real process activity — they were synthetic spans created by
-- the old "tag a dragged range" workflow and live on as tag_blocks only.
INSERT INTO process_tracks (process_name, process_path, window_title, start_time, end_time, duration_sec, is_idle)
SELECT process_name, process_path, window_title, start_time, end_time, duration_sec, is_idle
FROM focus_blocks
WHERE is_placeholder = 0;

-- Every tagged focus_block (placeholder or not) carries forward as a tag_block.
-- is_manual is the inverse of auto_tagged: blocks the rule engine tagged are
-- auto, everything else (explicit user assignment, manual tray submenu) is
-- manual.
INSERT INTO tag_blocks (tag_id, description, start_time, end_time, duration_sec, is_manual, personio_id, synced_at)
SELECT tag_id, description, start_time, end_time, duration_sec,
       CASE WHEN auto_tagged = 1 THEN 0 ELSE 1 END,
       personio_id, synced_at
FROM focus_blocks
WHERE tag_id IS NOT NULL;

DROP TABLE focus_blocks;
