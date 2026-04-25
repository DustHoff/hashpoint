// Package personio implements the Personio Attendance API client and the
// sync flow that maps tagged focus blocks into Personio attendance records.
//
// Auth via OAuth Client Credentials. The client secret is stored in the
// Windows Credential Manager (wincred) — never in the TOML config and never
// logged. On non-Windows hosts the secret is read from environment variables
// for development convenience.
package personio

import (
	"errors"
	"fmt"
	"os"
)

// CredentialTarget is the wincred target name used for the Personio secret.
const CredentialTarget = "TimeTracker.PersonioClientSecret"

// CredentialStore abstracts secret storage so it can be substituted in tests
// and on non-Windows builds.
type CredentialStore interface {
	GetSecret() (string, error)
	SetSecret(secret string) error
	DeleteSecret() error
}

// ErrSecretNotSet is returned when no client secret has been stored.
var ErrSecretNotSet = errors.New("personio: client secret not set")

// EnvCredentialStore reads the secret from PERSONIO_CLIENT_SECRET. Used on
// non-Windows hosts and in dev/test environments.
type EnvCredentialStore struct {
	EnvVar string
}

// GetSecret returns the secret from the environment.
func (e EnvCredentialStore) GetSecret() (string, error) {
	v := os.Getenv(e.envVar())
	if v == "" {
		return "", ErrSecretNotSet
	}
	return v, nil
}

// SetSecret is a no-op for env-backed stores.
func (e EnvCredentialStore) SetSecret(_ string) error {
	return fmt.Errorf("env credential store is read-only")
}

// DeleteSecret is a no-op for env-backed stores.
func (e EnvCredentialStore) DeleteSecret() error { return nil }

func (e EnvCredentialStore) envVar() string {
	if e.EnvVar == "" {
		return "PERSONIO_CLIENT_SECRET"
	}
	return e.EnvVar
}
