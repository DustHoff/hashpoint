// Package logging provides slog-based structured logging for the application.
//
// Production builds use a JSON handler, development builds a text handler.
// A rotating log file under %LOCALAPPDATA%\TimeTracker\log\ is set up by
// SetupFile; callers that don't want a file handler can use SetupConsole.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Mode controls log output formatting.
type Mode int

const (
	// ModeDev produces human-readable text output.
	ModeDev Mode = iota
	// ModeProd produces JSON output suitable for ingestion.
	ModeProd
)

// Options configures the logger setup.
type Options struct {
	Mode    Mode
	Level   slog.Level
	LogDir  string // Directory for rotating log files; empty = no file logging.
	Console bool   // Also write to stderr in addition to file.
}

// Setup initializes a slog default logger and returns the underlying writer
// used for the file sink (may be nil if LogDir is empty). Callers should close
// the writer at process exit if non-nil.
func Setup(opts Options) (io.Closer, error) {
	var writers []io.Writer
	var fileCloser io.Closer

	if opts.LogDir != "" {
		if err := os.MkdirAll(opts.LogDir, 0o700); err != nil {
			return nil, fmt.Errorf("create log dir: %w", err)
		}
		w, err := newRotatingWriter(opts.LogDir, "timetracker.log", 10*1024*1024, 5)
		if err != nil {
			return nil, fmt.Errorf("open log file: %w", err)
		}
		writers = append(writers, w)
		fileCloser = w
	}
	if opts.Console || len(writers) == 0 {
		writers = append(writers, os.Stderr)
	}

	out := io.MultiWriter(writers...)
	var handler slog.Handler
	hopts := &slog.HandlerOptions{Level: opts.Level}
	switch opts.Mode {
	case ModeProd:
		handler = slog.NewJSONHandler(out, hopts)
	default:
		handler = slog.NewTextHandler(out, hopts)
	}
	slog.SetDefault(slog.New(handler))
	return fileCloser, nil
}

// rotatingWriter is a tiny size-based log rotator. It is intentionally simple;
// for richer behavior swap in lumberjack later.
type rotatingWriter struct {
	mu       sync.Mutex
	dir      string
	base     string
	maxBytes int64
	keep     int
	f        *os.File
	written  int64
}

func newRotatingWriter(dir, base string, maxBytes int64, keep int) (*rotatingWriter, error) {
	w := &rotatingWriter{dir: dir, base: base, maxBytes: maxBytes, keep: keep}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) open() error {
	p := filepath.Join(w.dir, w.base)
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.f = f
	w.written = st.Size()
	return nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.written+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.written += int64(n)
	return n, err
}

func (w *rotatingWriter) rotate() error {
	if w.f != nil {
		if err := w.f.Close(); err != nil {
			return err
		}
	}
	stamp := time.Now().UTC().Format("20060102T150405")
	cur := filepath.Join(w.dir, w.base)
	rotated := filepath.Join(w.dir, fmt.Sprintf("%s.%s", w.base, stamp))
	if err := os.Rename(cur, rotated); err != nil && !os.IsNotExist(err) {
		return err
	}
	w.pruneOldLogs()
	return w.open()
}

func (w *rotatingWriter) pruneOldLogs() {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}
	type fi struct {
		name string
		mod  time.Time
	}
	var rotated []fi
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == w.base || len(name) <= len(w.base)+1 || name[:len(w.base)] != w.base {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		rotated = append(rotated, fi{name: name, mod: info.ModTime()})
	}
	if len(rotated) <= w.keep {
		return
	}
	// keep newest N; sort ascending by mod time and remove from front
	for i := 0; i < len(rotated)-1; i++ {
		for j := i + 1; j < len(rotated); j++ {
			if rotated[j].mod.Before(rotated[i].mod) {
				rotated[i], rotated[j] = rotated[j], rotated[i]
			}
		}
	}
	for _, r := range rotated[:len(rotated)-w.keep] {
		_ = os.Remove(filepath.Join(w.dir, r.name))
	}
}

// Close flushes and closes the underlying file.
func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
