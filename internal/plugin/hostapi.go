package plugin

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/dusthoff/hashpoint/plugin/sdk"
)

// boundHostAPI is the host-side sdk.HostAPI implementation handed to a
// single plugin via Plugin.Init. It is "bound" to the plugin's name so
// secret redemption refuses handles minted for a different plugin —
// defensive layer in case a leaked handle is replayed by a malicious or
// buggy plugin.
type boundHostAPI struct {
	pluginName string
	log        *slog.Logger
	handles    *handleRegistry
	settings   SettingsStore
}

// RedeemSecret resolves the handle to (plugin, key), confirms the plugin
// name matches the caller, and returns the plaintext from the settings
// store. Returns ErrUnknownSecretHandle for stale, cross-plugin, or
// since-deleted secrets — callers should treat that as a non-retryable
// configuration error.
func (a *boundHostAPI) RedeemSecret(ctx context.Context, h sdk.SecretHandle) (string, error) {
	entry, ok := a.handles.lookup(h)
	if !ok {
		return "", fmt.Errorf("%w: stale handle", sdk.ErrUnknownSecretHandle)
	}
	if entry.pluginName != a.pluginName {
		// Either a bug (we shipped the wrong handle) or a malicious
		// replay. Log the mismatch but do not reveal the other plugin's
		// name to the caller.
		a.log.Warn("plugin: cross-plugin secret redeem refused",
			"caller", a.pluginName, "handle_owner", entry.pluginName)
		return "", fmt.Errorf("%w: not owned by caller", sdk.ErrUnknownSecretHandle)
	}
	v, found, err := a.settings.GetSecret(ctx, a.pluginName, entry.secretKey)
	if err != nil {
		return "", fmt.Errorf("redeem %s/%s: %w", a.pluginName, entry.secretKey, err)
	}
	if !found {
		return "", fmt.Errorf("%w: secret no longer present", sdk.ErrUnknownSecretHandle)
	}
	return v, nil
}

// Log forwards a structured log line to the host's slog with an attached
// "plugin" attribute. Unknown levels degrade to Info. The plugin's name
// is filled in by the host — plugins must not echo it in fields.
func (a *boundHostAPI) Log(_ context.Context, level, message string, fields map[string]string) error {
	attrs := make([]any, 0, 2*len(fields))
	for k, v := range fields {
		// Refuse fields that would override the host-injected plugin name.
		if k == "plugin" {
			continue
		}
		attrs = append(attrs, k, v)
	}
	switch level {
	case "debug":
		a.log.Debug(message, attrs...)
	case "warn":
		a.log.Warn(message, attrs...)
	case "error":
		a.log.Error(message, attrs...)
	default:
		a.log.Info(message, attrs...)
	}
	return nil
}
