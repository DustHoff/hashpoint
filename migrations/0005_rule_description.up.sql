-- 0005: Optional default description for auto-tagging rules.
-- When a rule fires and the orchestrator opens an auto-tag block, the
-- (trimmed) description from the rule is copied onto tag_blocks.description
-- so it shows up in the timeline and gets appended to the Personio comment.

ALTER TABLE tagging_rules ADD COLUMN description TEXT;
