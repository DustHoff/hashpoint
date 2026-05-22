-- Revert 0010: drop the heartbeat column. last_seen carries no index or
-- constraint, so a plain DROP COLUMN is sufficient (SQLite >= 3.35, bundled
-- by modernc.org/sqlite). Recovery falls back to the start+idle_threshold
-- heuristic once the column is gone.

ALTER TABLE process_tracks DROP COLUMN last_seen;
