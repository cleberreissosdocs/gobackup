package winagentcore

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// LogPath is where the agent writes its log, next to config.json and
// jobs.json under %ProgramData%\GoBackup.
func LogPath() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = "."
	}
	return filepath.Join(base, "GoBackup", "agent.log")
}

// maxLogBytes is when the log rotates. Small on purpose: this file exists
// to answer "what happened at 3am last night", not to archive history,
// and it sits on the system drive of a server whose disk is somebody's
// production disk.
const maxLogBytes = 2 << 20 // 2 MiB

// SetupFileLogging points the standard logger at %ProgramData%\GoBackup\
// agent.log.
//
// Without this every log line the agent writes is lost: a Windows service
// has no console, so stdout goes nowhere. That silence is tolerable for a
// manual run (the operator is watching the screen) but not for scheduled
// ones -- a backup that fails at 3am would leave the operator nothing to
// read, and the scheduler's own decisions ("skipped, still running") would
// be invisible exactly when they matter.
//
// Failing to open the log is NOT fatal to the caller: an agent that still
// backs up but cannot write its log is far better than one that refuses to
// start because of a logging problem.
func SetupFileLogging() error {
	path := LogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("criar pasta do log: %w", err)
	}
	w, err := newRotatingWriter(path)
	if err != nil {
		return err
	}
	log.SetOutput(w)
	log.SetFlags(log.LstdFlags)
	return nil
}

// rotatingWriter appends to a file, renaming it aside once it passes
// maxLogBytes so the log cannot grow without bound on a machine nobody
// logs into for months. One generation is kept (agent.log.1): enough to
// survive a rotation that happens between the incident and the
// investigation, without turning into an archive.
type rotatingWriter struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

func newRotatingWriter(path string) (*rotatingWriter, error) {
	w := &rotatingWriter{path: path}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("abrir o log: %w", err)
	}
	size := int64(0)
	if info, err := f.Stat(); err == nil {
		size = info.Size()
	}
	w.f, w.size = f, size
	return nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return 0, os.ErrClosed
	}
	if w.size+int64(len(p)) > maxLogBytes {
		w.rotate()
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate must be called with the lock held. Any failure here is swallowed
// deliberately: losing rotation is a cosmetic problem, while returning an
// error from a log write could cascade into the caller.
func (w *rotatingWriter) rotate() {
	_ = w.f.Close()
	_ = os.Remove(w.path + ".1")
	_ = os.Rename(w.path, w.path+".1")
	if err := w.open(); err != nil {
		// Keep writing nowhere rather than crashing the agent.
		w.f = nil
	}
}

var _ io.Writer = (*rotatingWriter)(nil)
