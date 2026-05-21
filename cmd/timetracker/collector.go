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
	"syscall"

	"github.com/dusthoff/hashpoint/internal/collector"
	"github.com/dusthoff/hashpoint/internal/ipc"
	"github.com/dusthoff/hashpoint/internal/uisupervisor"
	"github.com/dusthoff/hashpoint/internal/winapi"
)

// collectorInstanceMutexName is the collector's own single-instance mutex,
// distinct from the monolith's so the two lock names never block each other
// during the migration from one model to the other (ADR §7.4).
const collectorInstanceMutexName = "Hashpoint.Collector"

// runCollector runs the headless collector half of the split (ADR 0001): it
// serves CollectorService over a per-user named pipe and owns the UI process
// lifecycle, spawning and respawning it on demand.
//
// WIP (Phase 1b): this wires the single-instance lock, the IPC server and the
// UI supervisor with a clean shutdown path. The domain — DB, tracker,
// orchestrator, tray, hotkey, power monitor, config — still lives in the
// monolith path (run) and moves here in the domain-ownership cut, together with
// the UI's method proxy (Phase 2). Until then, run() stays the shipped default.
func runCollector() error {
	lock, err := winapi.AcquireSingleInstanceLock(collectorInstanceMutexName)
	if err != nil {
		if errors.Is(err, winapi.ErrAlreadyRunning) {
			fmt.Fprintln(os.Stderr, "hashpoint: collector already running")
			return nil
		}
		return fmt.Errorf("acquire collector lock: %w", err)
	}
	defer func() { _ = lock.Release() }()

	ctx, cancel := signalContext()
	defer cancel()

	token, err := sessionToken()
	if err != nil {
		return err
	}
	pipeName := ipc.PipeName(token)

	hub := collector.NewEventHub(0)
	svc := collector.NewService(
		collector.VersionInfo{Version: version, Commit: commit, BuildDate: buildDate}, hub)

	srv, err := ipc.NewServer(pipeName, svc)
	if err != nil {
		return fmt.Errorf("start ipc server: %w", err)
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
		cmd := exec.CommandContext(c, exe, "--ui", "--pipe="+pipeName)
		// Surface the UI child's logs while developing the split. Production
		// logging becomes file-based with the domain move (Phase 2).
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd
	}, slog.Default())
	supDone := make(chan struct{})
	go func() { _ = sup.Run(ctx); close(supDone) }()

	select {
	case <-ctx.Done():
		slog.Info("collector: shutting down")
	case err := <-serveErr:
		cancel()
		<-supDone
		return fmt.Errorf("ipc server stopped unexpectedly: %w", err)
	}

	// Signalled shutdown: ctx is already cancelled, so the supervised UI is
	// being terminated; wait for it, then drain the server.
	<-supDone
	srv.Stop()
	<-serveErr
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
