package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PluginSettingsRepo is the SQL-backed PluginSettingsRepository. The
// two tables it owns (plugin_state + plugin_settings) are introduced in
// migration 0008. Secret rows (is_secret=1) are encrypted via the
// injected Cipher at write time and decrypted on read.
type PluginSettingsRepo struct {
	db     *sql.DB
	cipher Cipher
}

// NewPluginSettingsRepo wires the repo. Pass NewDPAPICipher() in
// production; tests use NoopCipher{}.
func NewPluginSettingsRepo(db *sql.DB, cipher Cipher) *PluginSettingsRepo {
	return &PluginSettingsRepo{db: db, cipher: cipher}
}

const (
	selectPluginEnabled = `SELECT enabled FROM plugin_state WHERE plugin_name = ?`

	upsertPluginEnabled = `INSERT INTO plugin_state (plugin_name, enabled, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT (plugin_name) DO UPDATE SET
			enabled = excluded.enabled,
			updated_at = CURRENT_TIMESTAMP`

	selectPluginFields = `SELECT key, value, is_secret
		FROM plugin_settings WHERE plugin_name = ?
		ORDER BY key ASC`

	selectPluginSecretValue = `SELECT value FROM plugin_settings
		WHERE plugin_name = ? AND key = ? AND is_secret = 1`

	upsertPluginField = `INSERT INTO plugin_settings (plugin_name, key, value, is_secret, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT (plugin_name, key) DO UPDATE SET
			value = excluded.value,
			is_secret = excluded.is_secret,
			updated_at = CURRENT_TIMESTAMP`

	deletePluginField     = `DELETE FROM plugin_settings WHERE plugin_name = ? AND key = ?`
	deletePluginFieldsAll = `DELETE FROM plugin_settings WHERE plugin_name = ?`
	deletePluginState     = `DELETE FROM plugin_state WHERE plugin_name = ?`
)

// GetEnabled returns the plugin's enable flag. A plugin that has never
// been touched (no plugin_state row) defaults to enabled — that lets a
// freshly-discovered plugin run on first start without an explicit
// opt-in from the user.
func (r *PluginSettingsRepo) GetEnabled(ctx context.Context, name string) (bool, error) {
	var enabled int64
	err := r.db.QueryRowContext(ctx, selectPluginEnabled, name).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read plugin enabled: %w", err)
	}
	return enabled != 0, nil
}

// SetEnabled upserts the plugin_state row. Idempotent.
func (r *PluginSettingsRepo) SetEnabled(ctx context.Context, name string, enabled bool) error {
	if _, err := r.db.ExecContext(ctx, upsertPluginEnabled, name, boolToInt(enabled)); err != nil {
		return fmt.Errorf("upsert plugin enabled: %w", err)
	}
	return nil
}

// GetFields returns the plain and secret values for the plugin's
// settings. Both maps are non-nil even when empty so callers can range
// over them without nil checks. Secret values are decrypted in-place;
// a decryption error aborts the whole call (a corrupted secret implies
// the plugin can't be configured correctly anyway — surface it).
func (r *PluginSettingsRepo) GetFields(ctx context.Context, name string) (plain map[string]string, secrets map[string]string, err error) {
	plain = map[string]string{}
	secrets = map[string]string{}
	rows, err := r.db.QueryContext(ctx, selectPluginFields, name)
	if err != nil {
		return nil, nil, fmt.Errorf("read plugin fields: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			key      string
			value    []byte
			isSecret int64
		)
		if err := rows.Scan(&key, &value, &isSecret); err != nil {
			return nil, nil, err
		}
		if isSecret != 0 {
			dec, err := r.cipher.Decrypt(value)
			if err != nil {
				return nil, nil, fmt.Errorf("decrypt plugin secret %q: %w", key, err)
			}
			secrets[key] = string(dec)
			continue
		}
		plain[key] = string(value)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return plain, secrets, nil
}

// GetSecret returns the decrypted value for a single secret field, with
// found=false when the row is absent or not flagged as a secret. Used
// by the plugin RedeemSecret reverse-RPC, which wants the absent-vs-
// error distinction without the import dependency a typed sentinel
// would imply.
func (r *PluginSettingsRepo) GetSecret(ctx context.Context, name, key string) (string, bool, error) {
	var value []byte
	err := r.db.QueryRowContext(ctx, selectPluginSecretValue, name, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read plugin secret: %w", err)
	}
	dec, err := r.cipher.Decrypt(value)
	if err != nil {
		return "", false, fmt.Errorf("decrypt plugin secret %q: %w", key, err)
	}
	return string(dec), true, nil
}

// SetField upserts a plain-text field.
func (r *PluginSettingsRepo) SetField(ctx context.Context, name, key, value string) error {
	return r.upsertField(ctx, name, key, []byte(value), false)
}

// SetSecretField upserts an encrypted field. The value is run through
// the repo's Cipher before being written.
func (r *PluginSettingsRepo) SetSecretField(ctx context.Context, name, key, value string) error {
	enc, err := r.cipher.Encrypt([]byte(value))
	if err != nil {
		return fmt.Errorf("encrypt plugin secret %q: %w", key, err)
	}
	return r.upsertField(ctx, name, key, enc, true)
}

func (r *PluginSettingsRepo) upsertField(ctx context.Context, name, key string, value []byte, isSecret bool) error {
	if _, err := r.db.ExecContext(ctx, upsertPluginField, name, key, value, boolToInt(isSecret)); err != nil {
		return fmt.Errorf("upsert plugin field %q: %w", key, err)
	}
	return nil
}

// DeleteField removes one row (plain or secret). Missing rows are not
// an error — callers can defensively delete fields the plugin no
// longer declares without checking first.
func (r *PluginSettingsRepo) DeleteField(ctx context.Context, name, key string) error {
	if _, err := r.db.ExecContext(ctx, deletePluginField, name, key); err != nil {
		return fmt.Errorf("delete plugin field %q: %w", key, err)
	}
	return nil
}

// Clear removes every row (settings + state) for the plugin. Used on
// uninstall so a future re-install starts from the manifest defaults.
func (r *PluginSettingsRepo) Clear(ctx context.Context, name string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, deletePluginFieldsAll, name); err != nil {
		return fmt.Errorf("delete plugin fields: %w", err)
	}
	if _, err := tx.ExecContext(ctx, deletePluginState, name); err != nil {
		return fmt.Errorf("delete plugin state: %w", err)
	}
	return tx.Commit()
}
