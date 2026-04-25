-- 0002: Per-block activity description.
-- Used for free-text annotations attached to a focus block (typically the same
-- text shared across a contiguous tag segment, set via the Timeline UI).

ALTER TABLE focus_blocks ADD COLUMN description TEXT;
