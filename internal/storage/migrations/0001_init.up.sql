-- Initial schema for Hashpoint TimeTracker.
-- All timestamps are stored in UTC.

CREATE TABLE tags (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    parent_id             INTEGER,
    name                  TEXT NOT NULL,
    description           TEXT,
    color                 TEXT,
    personio_project_id   TEXT,
    personio_activity_id  TEXT,
    sync_to_personio      INTEGER NOT NULL DEFAULT 1,
    created_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (parent_id) REFERENCES tags(id) ON DELETE CASCADE,
    UNIQUE (parent_id, name),
    CHECK (name GLOB '#[A-Za-z0-9]*')
);
CREATE INDEX idx_tags_parent ON tags(parent_id);

CREATE TABLE focus_blocks (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    process_name    TEXT NOT NULL,
    process_path    TEXT,
    window_title    TEXT NOT NULL,
    start_time      DATETIME NOT NULL,
    end_time        DATETIME,
    duration_sec    INTEGER,
    is_idle         INTEGER NOT NULL DEFAULT 0,
    tag_id          INTEGER,
    auto_tagged     INTEGER NOT NULL DEFAULT 0,
    personio_id     TEXT,
    synced_at       DATETIME,
    FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE SET NULL
);
CREATE INDEX idx_blocks_start ON focus_blocks(start_time);
CREATE INDEX idx_blocks_tag   ON focus_blocks(tag_id);
CREATE INDEX idx_blocks_open  ON focus_blocks(end_time) WHERE end_time IS NULL;

CREATE TABLE tagging_rules (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    match_field  TEXT NOT NULL CHECK (match_field IN ('process_name','window_title','both')),
    match_type   TEXT NOT NULL CHECK (match_type IN ('contains','equals','regex')),
    pattern      TEXT NOT NULL,
    tag_id       INTEGER NOT NULL,
    priority     INTEGER NOT NULL DEFAULT 0,
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
);
CREATE INDEX idx_rules_priority ON tagging_rules(priority DESC, enabled);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT
);
