-- 0006: parallel tracking of "communication processes" (Teams, Zoom, …).
--
-- Adds an is_communication flag to process_tracks. While the tracker
-- previously persisted exactly one (focused) row at a time, communication
-- tracks deliberately overlap with focused tracks and with each other when
-- multiple comm processes have visible windows simultaneously. The disjoint
-- invariant therefore now applies only to is_communication=0 rows; the
-- tracker remains the single writer and enforces that distinction.

ALTER TABLE process_tracks ADD COLUMN is_communication INTEGER NOT NULL DEFAULT 0;

-- Quickly find open communication tracks (one row per visible comm window)
-- so each tick can reconcile against the live window enumeration.
CREATE INDEX idx_process_tracks_comm_open
    ON process_tracks(is_communication, end_time)
    WHERE end_time IS NULL;
