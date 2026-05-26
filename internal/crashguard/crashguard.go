// Package crashguard records abnormal terminations so every crash leaves a
// trace in the application log.
//
// It provides two complementary mechanisms:
//
//   - Recover/Safe install a deferred panic handler at goroutine entry points.
//     A recovered panic is logged at Error with the panic value and a full
//     stack, then swallowed so a single faulty goroutine does not take the
//     whole process down — the "resilient where safe" policy. RecoverFatal is
//     the one exception: it re-raises after logging, used at the process entry
//     point where there is no surrounding goroutine left to keep alive.
//   - Marker (see marker.go) is a per-process-role sentinel file written at
//     startup and removed only on a clean shutdown. Because an abrupt kill
//     (OS suspend teardown, SIGKILL, power loss) leaves the file behind, the
//     next startup detects that the previous run did not exit cleanly and logs
//     it — the only way to capture terminations that run no Go code.
//
// All crash records share the stable "event" field values exported here so a
// downstream consumer (the Feedback issue builder) can pick them out of the
// JSON log.
package crashguard

import (
	"fmt"
	"log/slog"
	"runtime/debug"
)

// Event field values for crash log records. Kept stable because
// internal/feedback scans the JSON log for them.
const (
	// EventPanic marks a recovered Go panic (Recover/Safe/RecoverFatal).
	EventPanic = "panic"
	// EventUncleanShutdown marks a previous run that left its marker behind.
	EventUncleanShutdown = "unclean_shutdown"
	// EventUICrash marks a UI child process that exited unexpectedly.
	EventUICrash = "ui_crash"
)

// Recover is the deferred panic handler for a guarded goroutine. On a panic it
// logs the value and a full stack at Error, then returns — swallowing the panic
// so the goroutine unwinds to its caller instead of crashing the process. Use
// it as the first statement of a goroutine:
//
//	go func() {
//		defer crashguard.Recover(logger, "tracker-run")
//		// ...
//	}()
func Recover(logger *slog.Logger, name string) {
	if r := recover(); r != nil {
		logPanic(logger, name, r, false)
	}
}

// Safe runs fn under Recover so a panic in fn is logged and swallowed rather
// than propagated. Intended for wrapping a single loop iteration (e.g. one
// tracker tick) so one bad iteration does not end the loop.
func Safe(logger *slog.Logger, name string, fn func()) {
	defer Recover(logger, name)
	fn()
}

// RecoverFatal is the deferred handler for the process entry point. It logs the
// panic like Recover but re-raises it so the process still exits non-zero after
// the record is durably written. It reads slog.Default at panic time, so it
// captures the file logger installed during startup rather than whatever was
// default when the defer was registered.
func RecoverFatal() {
	if r := recover(); r != nil {
		logPanic(slog.Default(), "main", r, true)
		panic(r)
	}
}

func logPanic(logger *slog.Logger, name string, r any, fatal bool) {
	if logger == nil {
		logger = slog.Default()
	}
	logger.Error("recovered panic",
		"event", EventPanic,
		"goroutine", name,
		"fatal", fatal,
		"cause", fmt.Sprint(r),
		"stack", string(debug.Stack()),
	)
}
