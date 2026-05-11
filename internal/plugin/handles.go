package plugin

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/onesi/hashpoint/internal/plugin/sdk"
)

// handleRegistry maps random opaque tokens to (pluginName, secretKey)
// pairs. Plugins receive handles in PluginConfig.Secrets at Configure()
// time and redeem them via HostAPI.RedeemSecret() — the host resolves
// the pair via the registry, fetches the plaintext from SecretStore,
// and returns it.
//
// All entries are in-memory only: a host restart re-mints every handle,
// so a leaked handle dies on the next start. Per-plugin revocation
// (revokeFor) drops every entry for a single plugin when it is reloaded
// or torn down.
type handleRegistry struct {
	mu      sync.RWMutex
	entries map[string]handleEntry
}

type handleEntry struct {
	pluginName string
	secretKey  string
}

func newHandleRegistry() *handleRegistry {
	return &handleRegistry{entries: map[string]handleEntry{}}
}

// mint allocates a fresh random handle for the (plugin, key) pair. The
// handle is 128-bit hex; collisions are astronomically unlikely but the
// caller still treats the return as authoritative (no de-duplication).
func (r *handleRegistry) mint(pluginName, secretKey string) sdk.SecretHandle {
	h := generateHandle()
	r.mu.Lock()
	r.entries[h] = handleEntry{pluginName: pluginName, secretKey: secretKey}
	r.mu.Unlock()
	return sdk.SecretHandle(h)
}

// lookup returns the entry for the given handle and whether it was found.
func (r *handleRegistry) lookup(h sdk.SecretHandle) (handleEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[string(h)]
	return e, ok
}

// revokeFor drops every entry tied to pluginName. Called when a plugin
// is reloaded or stopped — any in-flight handles the plugin still holds
// become invalid and RedeemSecret returns ErrUnknownSecretHandle.
func (r *handleRegistry) revokeFor(pluginName string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for h, e := range r.entries {
		if e.pluginName == pluginName {
			delete(r.entries, h)
		}
	}
}

func generateHandle() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand reads from a system source; failure here means the
		// OS is broken in ways the rest of the program can't recover from.
		panic("plugin: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
