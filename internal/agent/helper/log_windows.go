//go:build windows

package helper

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// logWriter is a size-capped, single-rotation writer for the helper log at
// %LOCALAPPDATA%\NEXMIS\helper.log (the service's data dir is not writable by
// the interactive user).
type logWriter struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		if err := w.open(); err != nil {
			return len(p), nil // never fail the caller on logging
		}
	}
	if w.size+int64(len(p)) > w.max {
		w.rotate()
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *logWriter) open() error {
	if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	w.f = f
	if fi, err := f.Stat(); err == nil {
		w.size = fi.Size()
	}
	return nil
}

func (w *logWriter) rotate() {
	if w.f != nil {
		w.f.Close()
		w.f = nil
	}
	_ = os.Remove(w.path + ".1")
	_ = os.Rename(w.path, w.path+".1")
	w.size = 0
	_ = w.open()
}

// newLogger builds the helper's slog.Logger writing to the rolling file.
func newLogger() *slog.Logger {
	dir := os.Getenv("LOCALAPPDATA")
	if dir == "" {
		dir = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
	}
	w := &logWriter{path: filepath.Join(dir, "NEXMIS", "helper.log"), max: 1 << 20}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// recoverPanic logs a recovered panic so a single feature failure never crashes
// the helper. Use as `defer recoverPanic(log, "feature")`.
func recoverPanic(log *slog.Logger, where string) {
	if r := recover(); r != nil {
		if log != nil {
			log.Error("recovered panic", "where", where, "panic", fmt.Sprint(r))
		}
	}
}
