-- 0007 down: drop on-call documentation tables.

DROP INDEX IF EXISTS idx_oncall_subs_status;
DROP INDEX IF EXISTS idx_oncall_subs_doc;
DROP TABLE IF EXISTS oncall_submissions;

DROP INDEX IF EXISTS idx_oncall_docs_stale;
DROP INDEX IF EXISTS idx_oncall_docs_block;
DROP TABLE IF EXISTS oncall_documentations;
