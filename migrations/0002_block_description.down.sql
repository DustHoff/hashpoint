-- 0002 rollback: drop the per-block description column.

ALTER TABLE focus_blocks DROP COLUMN description;
