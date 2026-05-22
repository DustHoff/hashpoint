// Package ipc implements the local transport between the headless collector
// process and the on-demand Wails UI process (ADR 0001). The collector serves
// CollectorService over a per-user Windows named pipe; the UI dials it. gRPC
// rides directly on the pipe via a custom listener/dialer, so there is no TCP
// port to allocate and no other local user can reach the channel.
//
// The package is Windows-only at runtime; non-Windows builds compile against
// stubs (pipe_other.go) so linting and CI on Linux still typecheck the tree.
package ipc

// ProtocolVersion is the collector↔UI contract version, surfaced via
// CollectorService.GetVersion. Bump it on any incompatible change to the
// service so a stale UI — e.g. one spawned right after an in-place update,
// before the old collector is replaced — refuses to attach.
const ProtocolVersion = 1

// PipeName returns the named-pipe path for the given per-user session token.
// The collector mints a random token at startup, opens the pipe under this
// name, and hands the UI the exact name to dial via its launch arguments; the
// token keeps the path unguessable in addition to the pipe's user-only ACL.
func PipeName(token string) string {
	return `\\.\pipe\hashpoint-collector-` + token
}
