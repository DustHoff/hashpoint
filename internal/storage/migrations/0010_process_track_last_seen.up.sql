-- 0010: heartbeat column for crash-recovery accuracy.
--
-- While a process track is open, the tracker bumps last_seen on every poll
-- tick the track is confirmed active (not idle, not paused). On the next
-- start, recovery closes any track left open by a crash at its last_seen
-- instead of the start+idle_threshold heuristic — bounding the worst-case
-- data loss from hours (silent OS kill / WebView2 crash, see issue #21) to a
-- single poll interval.
--
-- Nullable with no default: rows written before this migration, and brand-new
-- tracks not yet touched, fall back cleanly to the legacy heuristic.

ALTER TABLE process_tracks ADD COLUMN last_seen DATETIME;
