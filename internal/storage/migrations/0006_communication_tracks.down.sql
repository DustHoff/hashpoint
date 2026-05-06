-- 0006 down: drop the communication-tracking column and its index.
DROP INDEX IF EXISTS idx_process_tracks_comm_open;
ALTER TABLE process_tracks DROP COLUMN is_communication;
