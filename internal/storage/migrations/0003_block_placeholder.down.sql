-- 0003 rollback: drop the placeholder marker column.

ALTER TABLE focus_blocks DROP COLUMN is_placeholder;
