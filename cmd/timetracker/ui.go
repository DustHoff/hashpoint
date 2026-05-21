package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	hashpoint "github.com/dusthoff/hashpoint"
	"github.com/dusthoff/hashpoint/internal/ipc"
	"github.com/dusthoff/hashpoint/internal/ipc/collectorpb"
	"github.com/dusthoff/hashpoint/internal/uiproxy"
	wails "github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// handshakeTimeout bounds the initial connect + version exchange with the
// collector. The collector creates the pipe before spawning us, so a healthy
// launch connects well within this; exceeding it means the collector is gone.
const handshakeTimeout = 10 * time.Second

// runUI runs the Wails UI half of the split (ADR 0001): a throwaway shell the
// collector spawns. It connects to the collector over the named pipe, verifies
// protocol compatibility, then binds a thin proxy (uiproxy.App, whose methods
// forward to the collector via Invoke) in place of the in-process app.App and
// renders the unchanged frontend. The collector→UI event stream is re-emitted
// onto the Wails runtime so the frontend's EventsOn handlers keep working.
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

	proxy := uiproxy.New(cli)

	// The event pump lives for the Wails session; cancelled on shutdown so the
	// collector's Events stream is released.
	eventCtx, eventCancel := context.WithCancel(context.Background())
	defer eventCancel()

	return wails.Run(&options.App{
		Title:            "Hashpoint TimeTracker",
		Width:            1200,
		Height:           800,
		MinWidth:         800,
		MinHeight:        600,
		WindowStartState: options.Maximised,
		AssetServer:      &assetserver.Options{Assets: hashpoint.Frontend},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup: func(ctx context.Context) {
			go pumpEvents(eventCtx, cli, ctx)
		},
		OnShutdown: func(context.Context) {
			eventCancel()
		},
		HideWindowOnClose: true,
		Bind:              []any{proxy},
	})
}

// pumpEvents subscribes to the collector's event stream and re-emits each event
// onto the Wails runtime, so the frontend receives them exactly as it did in
// the monolith. The JSON payload is forwarded verbatim (re-marshalled to the
// original object by Wails); no-payload events are emitted without data.
func pumpEvents(ctx context.Context, cli *ipc.Client, uiCtx context.Context) {
	stream, err := cli.Events(ctx, &collectorpb.EventsRequest{})
	if err != nil {
		slog.Warn("ui: subscribe to collector events failed", "err", err)
		return
	}
	for {
		ev, err := stream.Recv()
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("ui: collector event stream ended", "err", err)
			}
			return
		}
		if len(ev.JsonPayload) == 0 {
			wailsruntime.EventsEmit(uiCtx, ev.Name)
		} else {
			wailsruntime.EventsEmit(uiCtx, ev.Name, json.RawMessage(ev.JsonPayload))
		}
	}
}
