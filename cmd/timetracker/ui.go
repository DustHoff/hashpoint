package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/dusthoff/hashpoint/internal/ipc"
	"github.com/dusthoff/hashpoint/internal/ipc/collectorpb"
)

// handshakeTimeout bounds the initial connect + version exchange with the
// collector. The collector creates the pipe before spawning us, so a healthy
// launch connects well within this; exceeding it means the collector is gone.
const handshakeTimeout = 10 * time.Second

// runUI runs the Wails UI half of the split (ADR 0001): a throwaway shell the
// collector spawns on demand. It connects to the collector over the named pipe
// and verifies protocol compatibility before rendering.
//
// WIP (Phase 1b): this performs the connect + GetVersion handshake, then idles
// until terminated so the collector's supervisor keeps it alive (rather than
// respawn-looping). The app.App method proxy that forwards the ~69 RPCs to the
// collector and the actual wails.Run window arrive in Phase 2; until then --ui
// renders nothing and run() stays the shipped default.
func runUI(pipeName string) error {
	if pipeName == "" {
		return fmt.Errorf("ui mode requires --pipe=<name> from the collector")
	}

	dialCtx, cancelDial := context.WithTimeout(context.Background(), handshakeTimeout)
	defer cancelDial()

	cli, err := ipc.Dial(dialCtx, pipeName)
	if err != nil {
		return fmt.Errorf("connect to collector: %w", err)
	}
	defer func() { _ = cli.Close() }()

	info, err := cli.GetVersion(dialCtx, &collectorpb.GetVersionRequest{})
	if err != nil {
		return fmt.Errorf("collector handshake: %w", err)
	}
	if info.ProtocolVersion != ipc.ProtocolVersion {
		return fmt.Errorf("protocol mismatch: collector %d, ui %d — update did not replace both halves",
			info.ProtocolVersion, ipc.ProtocolVersion)
	}
	slog.Info("ui: connected to collector",
		"collector_version", info.Version, "protocol", info.ProtocolVersion)

	// TODO(phase2): build the app.App IPC proxy (forwarding Anhang A RPCs and
	// re-emitting the Events stream onto the wails runtime) and run wails.Run
	// here. For now idle until the collector terminates us, so the supervisor
	// treats this as a healthy long-lived child.
	ctx, cancel := signalContext()
	defer cancel()
	<-ctx.Done()
	return nil
}
