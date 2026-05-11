package plugin

import (
	"errors"
	"fmt"

	"github.com/danieljoos/wincred"
)

// SecretStore is what the host uses to persist per-plugin secrets. The
// production implementation is WinCredStore (Windows Credential Manager,
// User-scope); tests use InMemorySecretStore.
//
// Targets are namespaced per plugin: SecretTarget("oncall-jira", "api_token")
// → "TimeTracker:plugin:oncall-jira:api_token". The host generates and
// owns those strings; plugins never see them.
type SecretStore interface {
	Get(pluginName, key string) (string, error)
	Set(pluginName, key, value string) error
	Delete(pluginName, key string) error
}

// ErrSecretNotFound is returned by SecretStore.Get when no entry exists
// for the (pluginName, key) pair.
var ErrSecretNotFound = errors.New("plugin: secret not found")

// SecretTarget returns the credential-manager target string for a
// (plugin, key) pair. Exported so tests / migrations can build the same
// string deterministically.
func SecretTarget(pluginName, key string) string {
	return fmt.Sprintf("TimeTracker:plugin:%s:%s", pluginName, key)
}

// WinCredStore is the production SecretStore backed by Windows Credential
// Manager via github.com/danieljoos/wincred. Reads & writes are CurrentUser
// scope; the secret material is DPAPI-protected by the OS.
type WinCredStore struct{}

// Get returns the secret for (pluginName, key) or ErrSecretNotFound.
func (WinCredStore) Get(pluginName, key string) (string, error) {
	target := SecretTarget(pluginName, key)
	cred, err := wincred.GetGenericCredential(target)
	if err != nil {
		// wincred returns "Element not found." (Windows ERROR_NOT_FOUND).
		// Translate to a typed sentinel so callers can errors.Is it.
		return "", fmt.Errorf("%w: %s", ErrSecretNotFound, target)
	}
	if cred == nil {
		return "", fmt.Errorf("%w: %s", ErrSecretNotFound, target)
	}
	return string(cred.CredentialBlob), nil
}

// Set writes the secret. Overwrites any previous value.
func (WinCredStore) Set(pluginName, key, value string) error {
	target := SecretTarget(pluginName, key)
	cred := wincred.NewGenericCredential(target)
	cred.UserName = pluginName
	cred.CredentialBlob = []byte(value)
	cred.Persist = wincred.PersistLocalMachine
	if err := cred.Write(); err != nil {
		return fmt.Errorf("write secret %q: %w", target, err)
	}
	return nil
}

// Delete removes the secret. Missing entries are not an error.
func (WinCredStore) Delete(pluginName, key string) error {
	target := SecretTarget(pluginName, key)
	cred, err := wincred.GetGenericCredential(target)
	if err != nil || cred == nil {
		return nil
	}
	if err := cred.Delete(); err != nil {
		return fmt.Errorf("delete secret %q: %w", target, err)
	}
	return nil
}

// InMemorySecretStore is a SecretStore for tests. Not safe for concurrent
// use — wrap with sync.Mutex if needed.
type InMemorySecretStore struct {
	values map[string]string
}

// NewInMemorySecretStore returns a fresh in-memory store.
func NewInMemorySecretStore() *InMemorySecretStore {
	return &InMemorySecretStore{values: map[string]string{}}
}

// Get returns the value or ErrSecretNotFound.
func (s *InMemorySecretStore) Get(pluginName, key string) (string, error) {
	v, ok := s.values[SecretTarget(pluginName, key)]
	if !ok {
		return "", fmt.Errorf("%w: %s/%s", ErrSecretNotFound, pluginName, key)
	}
	return v, nil
}

// Set writes the value.
func (s *InMemorySecretStore) Set(pluginName, key, value string) error {
	s.values[SecretTarget(pluginName, key)] = value
	return nil
}

// Delete removes the value. Missing entries are not an error.
func (s *InMemorySecretStore) Delete(pluginName, key string) error {
	delete(s.values, SecretTarget(pluginName, key))
	return nil
}
