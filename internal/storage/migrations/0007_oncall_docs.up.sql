-- 0007: on-call ("Rufbereitschaft") documentation backing tables.
--
-- Whenever a tag block overlaps off-hours (per WorkScheduleConfig) AND its
-- tag is in the configured on-call set, the orchestrator enqueues a row in
-- oncall_documentations with status='pending'. The user fills out the form
-- on the Rufbereitschaft tab (application, incident_type, solution) and
-- submits — at which point the plugin host fans the payload out to every
-- installed plugin advertising the oncall_documentation capability, with
-- one oncall_submissions row tracking the result per plugin.
--
-- All times UTC; status is a roll-up computed by Go from the per-plugin
-- rows (no roll-up column to avoid two-source-of-truth drift).

CREATE TABLE oncall_documentations (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    block_id        INTEGER NOT NULL,
    -- Tag at the moment the doc was first enqueued. The current tag may
    -- drift later (user re-tag, resize out of off-hours); when that
    -- happens the doc is marked stale and the user is asked to confirm
    -- via a banner. Storing the original tag keeps the audit trail clean.
    tag_at_creation INTEGER NOT NULL,
    stale           INTEGER NOT NULL DEFAULT 0,
    application     TEXT    NOT NULL DEFAULT '',
    -- '' (draft), 'planned_maintenance', or 'service_disruption'
    incident_type   TEXT    NOT NULL DEFAULT '',
    solution        TEXT    NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (block_id) REFERENCES tag_blocks(id) ON DELETE CASCADE,
    UNIQUE (block_id)
);
CREATE INDEX idx_oncall_docs_block ON oncall_documentations(block_id);
CREATE INDEX idx_oncall_docs_stale ON oncall_documentations(stale) WHERE stale = 1;

CREATE TABLE oncall_submissions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    doc_id        INTEGER NOT NULL,
    plugin_name   TEXT    NOT NULL,
    -- 'pending' (queued / in flight), 'submitted' (plugin returned ok),
    -- 'failed' (plugin returned an error). Retry only re-dispatches
    -- rows in state 'failed' — 'submitted' is final to avoid duplicate
    -- tickets in the downstream system.
    status        TEXT    NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','submitted','failed')),
    external_ref  TEXT,
    external_url  TEXT,
    last_error    TEXT,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    submitted_at  DATETIME,
    FOREIGN KEY (doc_id) REFERENCES oncall_documentations(id) ON DELETE CASCADE,
    UNIQUE (doc_id, plugin_name)
);
CREATE INDEX idx_oncall_subs_doc    ON oncall_submissions(doc_id);
CREATE INDEX idx_oncall_subs_status ON oncall_submissions(status);
