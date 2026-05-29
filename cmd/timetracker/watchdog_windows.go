//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/dusthoff/hashpoint/internal/crashguard"
	"github.com/dusthoff/hashpoint/internal/storage"
	"github.com/dusthoff/hashpoint/internal/watchdog"
	"github.com/dusthoff/hashpoint/internal/winapi"
)

const (
	watchdogServiceName = "HashpointWatchdog"
	watchdogDisplayName = "Hashpoint Watchdog"
	watchdogDescription = "Überwacht den Hashpoint-Collector, schließt offene Tag-Blöcke bei einem Absturz und startet den Collector neu."
	// eventlogResetPeriodSec is the SC failure-action reset window (1 day).
	eventlogResetPeriodSec = 86400
)

// runWatchdog is the entry point for --watchdog. An "install"/"uninstall"
// sub-argument (de)registers the Windows service (a dev convenience; the MSI
// normally installs it). Under the Service Control Manager it runs as a
// service; otherwise it runs the monitor interactively for debugging.
func runWatchdog() error {
	for _, a := range os.Args[1:] {
		switch a {
		case "install":
			return installWatchdogService()
		case "uninstall":
			return uninstallWatchdogService()
		}
	}

	isService, err := svc.IsWindowsService()
	if err != nil {
		return fmt.Errorf("determine service mode: %w", err)
	}
	if isService {
		return runWatchdogService()
	}

	ctx, cancel := signalContext()
	defer cancel()
	slog.Info("watchdog: running interactively (not a service)")
	return newWatchdogMonitor(slog.Default()).Run(ctx)
}

// newWatchdogMonitor wires the platform probe and the storage-backed tag-block
// closer into a watchdog.Monitor.
func newWatchdogMonitor(logger *slog.Logger) *watchdog.Monitor {
	closeBlocks := func(ctx context.Context, t watchdog.Target, at time.Time) (int, error) {
		db, err := storage.Open(ctx, t.DBFile)
		if err != nil {
			return 0, fmt.Errorf("open db: %w", err)
		}
		defer func() { _ = db.Close() }()
		return watchdog.CloseOpenBlocks(ctx, storage.NewTagBlockRepo(db), at)
	}
	return watchdog.New(windowsProbe{}, closeBlocks, logger)
}

// windowsProbe implements watchdog.Probe using the active console session's
// user token to resolve the interactive user's paths and to relaunch the
// collector into that session — neither of which a Session-0 service can do
// through its own environment or a Local\ named object.
type windowsProbe struct{}

// ResolveTarget locates the interactive user's data dir, DB, collector marker
// and the executable to relaunch, via the active console session's user token.
func (windowsProbe) ResolveTarget(_ context.Context) (watchdog.Target, error) {
	sid, err := winapi.ActiveConsoleSessionID()
	if err != nil {
		if errors.Is(err, winapi.ErrNoActiveSession) {
			return watchdog.Target{}, watchdog.ErrNoActiveSession
		}
		return watchdog.Target{}, fmt.Errorf("active console session: %w", err)
	}
	tok, err := winapi.UserToken(sid)
	if err != nil {
		return watchdog.Target{}, fmt.Errorf("user token: %w", err)
	}
	defer func() { _ = tok.Close() }()

	env, err := winapi.UserEnvironment(tok)
	if err != nil {
		return watchdog.Target{}, fmt.Errorf("user environment: %w", err)
	}
	localAppData := env["LOCALAPPDATA"]
	if localAppData == "" {
		if profile := env["USERPROFILE"]; profile != "" {
			localAppData = filepath.Join(profile, "AppData", "Local")
		}
	}
	if localAppData == "" {
		return watchdog.Target{}, errors.New("cannot resolve interactive user LOCALAPPDATA")
	}
	exe, err := os.Executable()
	if err != nil {
		return watchdog.Target{}, fmt.Errorf("locate executable: %w", err)
	}
	// Build the per-user paths directly: config.ResolvePaths reads the *service*
	// account's environment, which would resolve the system profile instead.
	// The "TimeTracker"/"data.db" layout mirrors config.ResolvePaths.
	dataDir := filepath.Join(localAppData, "TimeTracker")
	return watchdog.Target{
		DataDir:    dataDir,
		DBFile:     filepath.Join(dataDir, "data.db"),
		MarkerPath: crashguard.MarkerPath(dataDir, crashguard.RoleCollector),
		Exe:        exe,
	}, nil
}

// CollectorAlive reads the collector marker and probes its PID for liveness,
// returning the marker's last-alive timestamp. crashguard.ErrMarkerAbsent and
// ErrMarkerCorrupt propagate so the monitor can skip the tick.
func (windowsProbe) CollectorAlive(_ context.Context, t watchdog.Target) (bool, time.Time, error) {
	snap, err := crashguard.ReadMarker(t.MarkerPath)
	if err != nil {
		return false, time.Time{}, err
	}
	alive, err := winapi.ProcessLiveness(snap.PID, filepath.Base(t.Exe), snap.StartUTC)
	if err != nil {
		return false, snap.LastAlive, fmt.Errorf("process liveness: %w", err)
	}
	return alive, snap.LastAlive, nil
}

// Relaunch starts the collector (--collector) in the interactive user session.
func (windowsProbe) Relaunch(_ context.Context, t watchdog.Target) error {
	sid, err := winapi.ActiveConsoleSessionID()
	if err != nil {
		if errors.Is(err, winapi.ErrNoActiveSession) {
			return watchdog.ErrNoActiveSession
		}
		return fmt.Errorf("active console session: %w", err)
	}
	tok, err := winapi.UserToken(sid)
	if err != nil {
		return fmt.Errorf("user token: %w", err)
	}
	defer func() { _ = tok.Close() }()

	if _, err := winapi.LaunchAsUser(tok, t.Exe, []string{"--collector"}, filepath.Dir(t.Exe)); err != nil {
		return fmt.Errorf("launch collector: %w", err)
	}
	return nil
}

// runWatchdogService hosts the monitor under the Service Control Manager,
// logging to the Windows event log (file logging would target the wrong user
// profile from Session 0).
func runWatchdogService() error {
	// Idempotent; LocalSystem has rights to register the source.
	_ = eventlog.InstallAsEventCreate(watchdogServiceName,
		windows.EVENTLOG_INFORMATION_TYPE|windows.EVENTLOG_WARNING_TYPE|windows.EVENTLOG_ERROR_TYPE)

	var logger *slog.Logger
	elog, err := eventlog.Open(watchdogServiceName)
	if err != nil {
		logger = slog.Default()
		logger.Warn("watchdog: event log unavailable — falling back to default logger", "err", err)
	} else {
		defer func() { _ = elog.Close() }()
		logger = slog.New(newEventlogHandler(elog, slog.LevelInfo))
	}

	if err := svc.Run(watchdogServiceName, &watchdogService{logger: logger}); err != nil {
		return fmt.Errorf("service run: %w", err)
	}
	return nil
}

// watchdogService adapts the monitor to the svc.Handler contract.
type watchdogService struct {
	logger *slog.Logger
}

// Execute runs the service: it starts the monitor on a cancellable context and
// drives the SCM state machine, cancelling the monitor on Stop/Shutdown.
func (s *watchdogService) Execute(_ []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer crashguard.Recover(s.logger, "watchdog-monitor")
		defer close(done)
		_ = newWatchdogMonitor(s.logger).Run(ctx)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: accepted}
	s.logger.Info("watchdog: service running")
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				s.logger.Info("watchdog: stopping")
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				changes <- svc.Status{State: svc.Stopped}
				return false, 0
			default:
				s.logger.Warn("watchdog: unexpected control request", "cmd", c.Cmd)
			}
		case <-done:
			// The monitor exited on its own (e.g. a recovered panic).
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		}
	}
}

// installWatchdogService registers the service with the SCM (dev convenience).
func installWatchdogService() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer func() { _ = m.Disconnect() }()

	if existing, err := m.OpenService(watchdogServiceName); err == nil {
		_ = existing.Close()
		return fmt.Errorf("service %q already installed", watchdogServiceName)
	}
	s, err := m.CreateService(watchdogServiceName, exe, mgr.Config{
		DisplayName: watchdogDisplayName,
		Description: watchdogDescription,
		StartType:   mgr.StartAutomatic,
		ServiceType: windows.SERVICE_WIN32_OWN_PROCESS,
	}, "--watchdog")
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}, eventlogResetPeriodSec); err != nil {
		slog.Warn("watchdog: could not set recovery actions", "err", err)
	}
	_ = eventlog.InstallAsEventCreate(watchdogServiceName,
		windows.EVENTLOG_INFORMATION_TYPE|windows.EVENTLOG_WARNING_TYPE|windows.EVENTLOG_ERROR_TYPE)
	slog.Info("watchdog: service installed", "name", watchdogServiceName)
	return nil
}

// uninstallWatchdogService stops and removes the service (dev convenience).
func uninstallWatchdogService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer func() { _ = m.Disconnect() }()

	s, err := m.OpenService(watchdogServiceName)
	if err != nil {
		return fmt.Errorf("open service: %w", err)
	}
	defer func() { _ = s.Close() }()

	if _, err := s.Control(svc.Stop); err != nil {
		slog.Warn("watchdog: stop on uninstall failed (may not be running)", "err", err)
	}
	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	_ = eventlog.Remove(watchdogServiceName)
	slog.Info("watchdog: service uninstalled", "name", watchdogServiceName)
	return nil
}

// eventlogHandler is a minimal slog.Handler that writes records to the Windows
// event log, mapping slog levels onto the Info/Warning/Error event types.
type eventlogHandler struct {
	elog  *eventlog.Log
	level slog.Level
	attrs []slog.Attr
}

func newEventlogHandler(elog *eventlog.Log, level slog.Level) *eventlogHandler {
	return &eventlogHandler{elog: elog, level: level}
}

// Enabled reports whether records at the given level are logged.
func (h *eventlogHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

// Handle formats the record (message plus attributes) and writes it to the
// event log at the matching severity.
func (h *eventlogHandler) Handle(_ context.Context, r slog.Record) error {
	var sb strings.Builder
	sb.WriteString(r.Message)
	for _, a := range h.attrs {
		fmt.Fprintf(&sb, " %s=%v", a.Key, a.Value)
	}
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&sb, " %s=%v", a.Key, a.Value)
		return true
	})
	const eventID = 1
	msg := sb.String()
	switch {
	case r.Level >= slog.LevelError:
		return h.elog.Error(eventID, msg)
	case r.Level >= slog.LevelWarn:
		return h.elog.Warning(eventID, msg)
	default:
		return h.elog.Info(eventID, msg)
	}
}

// WithAttrs returns a handler that prepends attrs to every record.
func (h *eventlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &clone
}

// WithGroup is a no-op: the event log is flat, so groups are ignored.
func (h *eventlogHandler) WithGroup(_ string) slog.Handler { return h }
