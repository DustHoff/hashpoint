package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dusthoff/hashpoint/internal/collector"
	"github.com/dusthoff/hashpoint/internal/config"
	"github.com/dusthoff/hashpoint/internal/ipc"
	"github.com/dusthoff/hashpoint/internal/ipc/collectorpb"
	"github.com/dusthoff/hashpoint/internal/uisupervisor"
	"github.com/dusthoff/hashpoint/internal/winapi"
)

// collectorInstanceMutexName is the collector's own single-instance mutex,
// distinct from the monolith's so the two lock names never block each other
// during the migration from one model to the other (ADR §7.4).
const collectorInstanceMutexName = "Hashpoint.Collector"

// runCollector runs the headless collector half of the split (ADR 0001): it
// owns the full domain (storage, tracker, orchestrator, sessions, hotkey,
// power monitor, config) headless, serves it to the UI through the generic
// Invoke RPC over a per-user named pipe, and owns the UI process lifecycle.
//
// Tray ownership and on-demand UI spawning (vs. the always-on supervision used
// here) land in the lifecycle step; the shipped default remains run().
func runCollector() error {
	lock, err := winapi.AcquireSingleInstanceLock(collectorInstanceMutexName)
	if err != nil {
		if errors.Is(err, winapi.ErrAlreadyRunning) {
			// A collector is already running (e.g. autostarted at login and
			// this is a Start-menu click): ask it to show the UI, then exit.
			return signalShowUI()
		}
		return fmt.Errorf("acquire collector lock: %w", err)
	}
	defer func() { _ = lock.Release() }()

	ctx, cancel := signalContext()
	defer cancel()

	paths, cfg, closeLog, err := bootstrap()
	if err != nil {
		return err
	}
	defer closeLog()

	// The collector has no Wails runtime, so the domain emits events onto the
	// IPC hub for the UI to re-emit.
	hub := collector.NewEventHub(0)
	d, err := buildDomain(ctx, paths, cfg, collector.NewEventSink(hub))
	if err != nil {
		return err
	}
	defer func() { _ = d.dbClose() }()
	if d.power != nil {
		defer func() { _ = d.power.Close() }()
	}

	// Domain init — orchestrator recovery, plugin host start, previous-day
	// Personio sync — runs in the collector now, not the UI. It also sets the
	// app's started flag so event emission to the hub is enabled.
	d.app.Startup(ctx)

	token, err := sessionToken()
	if err != nil {
		return err
	}
	pipeName := ipc.PipeName(token)
	svc := collector.NewService(
		collector.VersionInfo{Version: version, Commit: commit, BuildDate: buildDate},
		hub, collector.NewInvoker(d.app))

	srv, err := ipc.NewServer(pipeName, svc)
	if err != nil {
		return fmt.Errorf("start ipc server: %w", err)
	}
	// Publish the pipe name so a second launch (Start-menu click) can find this
	// collector and ask it to show the UI instead of starting a duplicate.
	if err := writePipeFile(paths, pipeName); err != nil {
		slog.Warn("collector: could not write pipe discovery file", "err", err)
	} else {
		defer removePipeFile(paths)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve() }()
	slog.Info("collector: serving", "pipe", pipeName)

	// Own the UI lifecycle: spawn ourselves as the UI shell pointed at our
	// pipe. CommandContext ties the child to ctx, so cancelling on shutdown
	// terminates it.
	exe, err := os.Executable()
	if err != nil {
		srv.Stop()
		return fmt.Errorf("locate executable: %w", err)
	}
	sup := uisupervisor.New(func(c context.Context) *exec.Cmd {
		cmd := exec.CommandContext(c, exe, "--ui", "--pipe="+pipeName) //nolint:gosec // own resolved executable, internal args only
		// Surface the UI child's logs while developing the split.
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd
	}, slog.Default())
	supDone := make(chan struct{})
	go func() { _ = sup.Run(ctx); close(supDone) }()

	// The tray lives in the collector (ADR): domain actions (pause, sync,
	// manual-tag) run in-process on d.app; "Öffnen"/"Hilfe" signal the UI
	// process via the hub, and "Beenden" cancels the collector context.
	trayAct := trayActions{
		open:     func() { hub.Publish(collector.EventShowUI, nil) },
		openHelp: func() { hub.Publish(collector.EventShowUI, nil); hub.Publish(uiHelpEvent, nil) },
		quit:     func() bool { cancel(); return false },
	}
	go runTray(ctx, d.app, trayAct, version)

	select {
	case <-ctx.Done():
		slog.Info("collector: shutting down")
	case err := <-serveErr:
		cancel()
		<-supDone
		d.shutdown(context.Background())
		return fmt.Errorf("ipc server stopped unexpectedly: %w", err)
	}

	// Signalled shutdown: ctx is already cancelled, so the supervised UI is
	// being terminated; wait for it, drain the server, then flush the domain.
	<-supDone
	srv.Stop()
	<-serveErr
	d.shutdown(context.Background())
	return nil
}

// signalContext returns a context cancelled on the first SIGINT/SIGTERM. Used
// by the collector and UI processes, which have no Wails lifecycle of their own.
func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		cancel()
	}()
	return ctx, cancel
}

// sessionToken returns a random hex token used to make the per-user pipe name
// unguessable in addition to its user-only ACL.
func sessionToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// pipeFileName is the per-user discovery file holding the running collector's
// pipe name, written under the data dir (user-only LOCALAPPDATA permissions).
const pipeFileName = "collector.pipe"

func pipeFilePath(paths config.Paths) string {
	return filepath.Join(paths.DataDir, pipeFileName)
}

func writePipeFile(paths config.Paths, name string) error {
	return os.WriteFile(pipeFilePath(paths), []byte(name), 0o600)
}

func removePipeFile(paths config.Paths) {
	if err := os.Remove(pipeFilePath(paths)); err != nil && !os.IsNotExist(err) {
		slog.Warn("collector: could not remove pipe discovery file", "err", err)
	}
}

// signalShowUI is taken when a collector is already running: it reads that
// collector's pipe name from the discovery file, dials it and asks it to show
// the UI, then exits. Every failure is best-effort (reported to stderr, never
// fatal) — a second launch must never disrupt the running collector.
func signalShowUI() error {
	paths, err := config.ResolvePaths()
	if err != nil {
		return fmt.Errorf("resolve paths: %w", err)
	}
	data, err := os.ReadFile(pipeFilePath(paths))
	if err != nil {
		fmt.Fprintln(os.Stderr, "hashpoint: collector already running (could not locate its pipe to show the UI)")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := ipc.Dial(ctx, string(data))
	if err != nil {
		fmt.Fprintln(os.Stderr, "hashpoint: collector already running (could not connect to show the UI)")
		return nil
	}
	defer func() { _ = cli.Close() }()
	if _, err := cli.ShowUI(ctx, &collectorpb.ShowUIRequest{}); err != nil {
		fmt.Fprintln(os.Stderr, "hashpoint: collector already running (show-UI request failed)")
	}
	return nil
}
