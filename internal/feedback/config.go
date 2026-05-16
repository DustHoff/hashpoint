// Package feedback implements the in-app "Feedback" tab: a GitHub App
// Device-Flow login plus an issue submitter that posts user-reported
// bugs (with optional sanitized log tail) into the upstream Hashpoint
// repository.
//
// The package is intentionally narrow:
//
//   - It talks to github.com only. No backend proxy, no telemetry.
//   - The OAuth client is a registered GitHub App with Device Flow
//     enabled. The App must be installed on RepoOwner/RepoName for
//     user-to-server tokens to be allowed to create issues.
//   - The User-to-Server access token (plus refresh token + expiry)
//     is persisted in the Windows Credential Manager via wincred.
//
// All exported types live here; the implementation is split across
// github_client.go (HTTP), token_store_{windows,other}.go (persistence),
// log_tail.go (sanitization), and body.go (Markdown rendering).
package feedback

// ClientID is the public Client ID of the Hashpoint feedback GitHub App.
// GitHub App client IDs are not secrets — they identify the app, not the
// user — so embedding the value in the binary is the documented pattern.
const ClientID = "Iv23livhMuISPM3JKmTg"

// RepoOwner is the GitHub login that owns the target repository.
const RepoOwner = "DustHoff"

// RepoName is the repository that receives feedback issues.
const RepoName = "hashpoint"

// CredentialTarget is the wincred entry name under which the access /
// refresh token blob is stored. Kept distinct from the Personio entry
// (TimeTracker.PersonioSession) so a logout from one feature never
// affects the other.
const CredentialTarget = "TimeTracker.GitHubFeedback"

// LogFileName is the active log file inside Paths.LogDir. Mirrors the
// constant used by internal/logging when opening the rotating writer.
const LogFileName = "timetracker.log"

// MaxLogTailBytes caps how much sanitized log text is embedded in the
// issue body. GitHub's hard limit on issue bodies is 65_536 chars; we
// stay well below it so the structured fields and the <details> block
// fit alongside the log.
const MaxLogTailBytes = 50 * 1024
