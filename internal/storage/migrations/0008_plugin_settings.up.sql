-- 0008: plugin configuration storage.
--
-- Plugins declare their config schema in their manifest (text / password
-- / boolean fields). The user's values are persisted here, one row per
-- (plugin, key) pair. Rows with is_secret=1 carry a DPAPI-encrypted
-- ciphertext in `value`; plain rows carry the UTF-8 representation
-- directly. Encryption/decryption is owned by the repo so callers
-- always work with cleartext strings.
--
-- plugin_state holds the user-facing enabled flag, decoupled from
-- plugin_settings so a disable→enable cycle preserves previously-
-- entered field values. A plugin that has never been touched has no
-- row in plugin_state and is treated as enabled by default.

CREATE TABLE plugin_state (
    plugin_name TEXT    PRIMARY KEY,
    enabled     INTEGER NOT NULL DEFAULT 1,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE plugin_settings (
    plugin_name TEXT    NOT NULL,
    key         TEXT    NOT NULL,
    value       BLOB    NOT NULL,
    is_secret   INTEGER NOT NULL DEFAULT 0,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (plugin_name, key)
);
CREATE INDEX idx_plugin_settings_plugin ON plugin_settings(plugin_name);
