package entra

import "errors"

// ErrNotConfigured signals that the Entra feature is dormant — typically
// because the user hasn't filled in client_id / tenant_id under
// Settings → Entra ID. Wails bindings short-circuit on this and return
// "feature off" to the UI rather than surfacing it as an error.
var ErrNotConfigured = errors.New("entra: client_id/tenant_id not configured")

// ErrSignedOut means the persistent cache holds no account at all — the
// user has never logged in on this machine, or has explicitly signed out.
// The UI should offer a Login button.
var ErrSignedOut = errors.New("entra: not signed in")

// ErrInteractiveRequired is returned by AcquireToken when the silent path
// fails (cache invalidated, refresh token rejected, CA-policy drift, MFA
// re-required) AND the caller did not authorise an interactive fallback.
// Caller-facing flow: catch this, ask the user "Sitzung abgelaufen — neu
// anmelden?" and on confirm call Login() again.
var ErrInteractiveRequired = errors.New("entra: interactive login required")
