// Package jobexec (control.go): lets the HTTP layer Cancel/Pause/Resume a
// job that's currently running through this Engine — whether it was
// started by the local web UI or by a run_job command relayed from the
// Central.
//
// Cancel works the same way for both job kinds: it cancels the job's
// context, which backup.Run (rsync) and dbjob.Run (pg_dump/pg_dumpall/
// mysqldump) both already honor via exec.CommandContext — the subprocess
// gets killed and the job reports "cancelled" instead of "failed".
//
// Pause/Resume is intentionally ONLY offered for file-mirror jobs: it
// sends SIGSTOP/SIGCONT to the running rsync process, a true OS-level
// pause where its network/disk I/O genuinely stops until resumed. A
// database dump is a single long-lived connection to the database server
// (pg_dump/mysqldump) — suspending that process for an unknown amount of
// time risks the server's own idle/statement timeout killing the
// connection from its side, silently truncating the backup when resumed.
// That failure mode is bad enough (and happens far enough after the fact)
// that it's not worth the feature; Cancel is always available instead.
package jobexec

import (
	"context"
	"fmt"
	"os"
)

// pauser wraps the OS process backing a running file-mirror job. The
// actual Pause()/Resume() signal-sending is platform-specific — see
// pauser_unix.go (real SIGSTOP/SIGCONT) and pauser_windows.go (stub: this
// package is only ever pulled into a Windows BUILD transitively, via
// internal/poller's shared wire-protocol types — the real Windows agent
// has its own cooperative pause mechanism in internal/winagentcore, never
// this one).

// jobControl is what Cancel/Pause/Resume act on for one in-flight
// execution, keyed by job ID string in Engine.running.
type jobControl struct {
	cancel context.CancelFunc
	kind   string // "files" | "database"
	pauser *pauser
	paused bool
}

// tryRegister starts tracking jobIDStr as running and returns a
// cancellable context plus a setter the caller invokes once the underlying
// rsync process has actually started (file jobs only — nil-safe to call
// for database jobs, which just won't get a pauser).
//
// ok is false when this job is ALREADY running, which is how a second
// execution is refused. The check and the insert happen under one lock
// hold, and the caller must invoke this SYNCHRONOUSLY before spawning the
// job goroutine: reserving inside the goroutine instead would let two
// run requests arriving together both pass the check before either
// registered, which is precisely the double-execution this prevents.
func (e *Engine) tryRegister(jobIDStr, kind string) (context.Context, func(*os.Process), bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running == nil {
		e.running = map[string]*jobControl{}
	}
	if _, already := e.running[jobIDStr]; already {
		return nil, nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	jc := &jobControl{cancel: cancel, kind: kind}
	if kind == "files" {
		jc.pauser = &pauser{}
	}
	e.running[jobIDStr] = jc
	return ctx, jc.pauser.setProcess, true
}

func (e *Engine) unregister(jobIDStr string) {
	e.mu.Lock()
	delete(e.running, jobIDStr)
	e.mu.Unlock()
}

func (e *Engine) control(jobIDStr string) (*jobControl, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	jc, ok := e.running[jobIDStr]
	return jc, ok
}

// Cancel aborts a running execution (file or database). The job's
// goroutine observes ctx.Done() via exec.CommandContext and reports status
// "cancelled".
func (e *Engine) Cancel(jobIDStr string) error {
	jc, ok := e.control(jobIDStr)
	if !ok {
		return fmt.Errorf("esta tarefa não está em execução")
	}
	jc.cancel()
	return nil
}

// Pause suspends a running file-mirror job's rsync process at the OS
// level. Returns an error (not supported) for database jobs — see the
// package doc comment for why.
func (e *Engine) Pause(jobIDStr string) error {
	jc, ok := e.control(jobIDStr)
	if !ok {
		return fmt.Errorf("esta tarefa não está em execução")
	}
	if jc.kind != "files" {
		return fmt.Errorf("pausar não é suportado para tarefas de banco de dados (risco de a conexão expirar e corromper o dump) — use cancelar")
	}
	if err := jc.pauser.Pause(); err != nil {
		return err
	}
	e.mu.Lock()
	jc.paused = true
	e.mu.Unlock()
	// The copy loop's own progress callback drives Registry updates, but
	// it stops firing entirely once the process is genuinely suspended —
	// without this, the UI would be stuck showing "running" forever
	// instead of "paused" until Resume lets at least one more file finish.
	if s, ok := e.Registry.Get(jobIDStr); ok {
		s.Status = "paused"
		e.Registry.Set(jobIDStr, s)
	}
	return nil
}

// Resume un-suspends a previously paused file-mirror job.
func (e *Engine) Resume(jobIDStr string) error {
	jc, ok := e.control(jobIDStr)
	if !ok {
		return fmt.Errorf("esta tarefa não está em execução")
	}
	if jc.kind != "files" {
		return fmt.Errorf("esta tarefa não suporta pausa")
	}
	if err := jc.pauser.Resume(); err != nil {
		return err
	}
	e.mu.Lock()
	jc.paused = false
	e.mu.Unlock()
	if s, ok := e.Registry.Get(jobIDStr); ok {
		s.Status = "running"
		e.Registry.Set(jobIDStr, s)
	}
	return nil
}
