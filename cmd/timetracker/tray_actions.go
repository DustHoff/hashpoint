package main

// uiHelpEvent must match app.helpOpenEvent ("help:open") — the frontend
// switches to the Help tab on it. Published by the collector's tray "Hilfe"
// since it cannot call the window-bound app.OpenHelpTab itself. The
// show-window control event is collector.EventShowUI.
const uiHelpEvent = "help:open"

// trayActions are the window/lifecycle callbacks the tray triggers, which
// differ between the monolith (drive the in-process Wails window) and the
// collector (signal the separate UI process and quit both). Domain actions
// (pause, sync, manual-tag) are not here — they call *app.App directly and
// work identically in both modes.
type trayActions struct {
	// open brings the UI to the foreground.
	open func()
	// openHelp brings the UI forward and opens the Help tab.
	openHelp func()
	// quit performs the "Beenden" action and reports whether the tray must
	// hard-stop itself (systray.Quit + os.Exit) because no graceful path took
	// over — the monolith returns true when Wails has not started; the
	// collector cancels its context and returns false (its run loop tears
	// down).
	quit func() bool
	// disarm removes the crash-sentinel marker before a hard os.Exit, which
	// would otherwise bypass the deferred Disarm and leave the marker behind —
	// making the next start misreport a clean quit as an unclean shutdown. May
	// be nil (e.g. the collector, which never hard-exits from the tray).
	disarm func()
}
