-- 0004 down: restore the unified focus_blocks table by merging process_tracks
-- and tag_blocks back into a single row per process event. Tag-blocks that did
-- not correspond to any process event (manual-only spans) become placeholder
-- rows so the legacy code path can still reach them.

CREATE TABLE focus_blocks (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    process_name    TEXT NOT NULL,
    process_path    TEXT,
    window_title    TEXT NOT NULL,
    start_time      DATETIME NOT NULL,
    end_time        DATETIME,
    duration_sec    INTEGER,
    is_idle         INTEGER NOT NULL DEFAULT 0,
    tag_id          INTEGER,
    auto_tagged     INTEGER NOT NULL DEFAULT 0,
    description     TEXT,
    personio_id     TEXT,
    synced_at       DATETIME,
    is_placeholder  INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE SET NULL
);
CREATE INDEX idx_blocks_start ON focus_blocks(start_time);
CREATE INDEX idx_blocks_tag   ON focus_blocks(tag_id);
CREATE INDEX idx_blocks_open  ON focus_blocks(end_time) WHERE end_time IS NULL;

-- Every process track becomes a focus_block; tagging columns are filled in
-- below where a tag_block covers the same time.
INSERT INTO focus_blocks (process_name, process_path, window_title, start_time, end_time, duration_sec, is_idle)
SELECT process_name, process_path, window_title, start_time, end_time, duration_sec, is_idle
FROM process_tracks;

-- Tag-blocks that have no overlapping process_track become placeholder rows.
INSERT INTO focus_blocks (process_name, window_title, start_time, end_time, duration_sec, tag_id, auto_tagged, description, personio_id, synced_at, is_placeholder)
SELECT '', '', tb.start_time, tb.end_time, tb.duration_sec, tb.tag_id,
       CASE WHEN tb.is_manual = 1 THEN 0 ELSE 1 END,
       tb.description, tb.personio_id, tb.synced_at, 1
FROM tag_blocks tb
WHERE NOT EXISTS (
    SELECT 1 FROM process_tracks pt
    WHERE pt.start_time < COALESCE(tb.end_time, '9999-01-01')
      AND COALESCE(pt.end_time, '9999-01-01') > tb.start_time
);

DROP TABLE tag_blocks;
DROP TABLE process_tracks;
