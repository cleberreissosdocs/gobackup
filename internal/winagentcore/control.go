// Package winagentcore (control.go): lets the Central Cancel/Pause/Resume
// a job currently running through this Windows agent's Executor — the
// same cancel_job/pause_job/resume_job commands the Linux agent's
// internal/poller.Executor understands.
//
// There's no external rsync process to SIGSTOP here (mirror.go/sshdest.go
// are plain Go copy loops), so Pause/Resume for a Windows file-mirror job
// is cooperative instead of OS-level: PauseGate.Wait is checked between
// each file, blocking the copy loop there until Resume or Cancel. Cancel
// itself works for both job kinds via context cancellation — the copy
// loop checks ctx.Done() too, and for database jobs it's threaded into
// exec.CommandContext the same way the Linux agent's dbdump package does,
// killing pg_dump/mysqldump outright.
//
// Pause is deliberately NOT offered for database jobs — see
// internal/jobexec/control.go's package doc for the (platform-independent)
// reasoning: a long-suspended dump connection risks the database server's
// own idle timeout silently truncating the backup.
package winagentcore

import (
	"context"
	"fmt"
	"sync"
)

// PauseGate is a cooperative pause/resume gate for a loop that processes
// work items one at a time (one per file, here).
type PauseGate struct {
	mu       sync.Mutex
	paused   bool
	resumeCh chan struct{}
}

func NewPauseGate() *PauseGate { return &PauseGate{} }

func (g *PauseGate) Pause() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paused {
		return
	}
	g.paused = true
	g.resumeCh = make(chan struct{})
}

func (g *PauseGate) Resume() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.paused {
		return
	}
	g.paused = false
	close(g.resumeCh)
}

// Wait blocks the caller while paused, returning early if ctx is
// cancelled (so a Cancel during a pause doesn't hang forever).
func (g *PauseGate) Wait(ctx context.Context) {
	g.mu.Lock()
	if !g.paused {
		g.mu.Unlock()
		return
	}
	ch := g.resumeCh
	g.mu.Unlock()
	select {
	case <-ch:
	case <-ctx.Done():
	}
}

// JobControl is what Cancel/Pause/Resume act on for one in-flight
// execution on this agent.
type JobControl struct {
	Cancel context.CancelFunc
	Kind   string     // "files" | "database"
	Pause  *PauseGate // non-nil only for file jobs
}

func (e *Executor) registerControl(parent context.Context, jobID, kind string) (context.Context, *JobControl) {
	ctx, cancel := context.WithCancel(parent)
	jc := &JobControl{Cancel: cancel, Kind: kind}
	if kind == "files" {
		jc.Pause = NewPauseGate()
	}
	e.mu.Lock()
	if e.controls == nil {
		e.controls = map[string]*JobControl{}
	}
	e.controls[jobID] = jc
	e.mu.Unlock()
	return ctx, jc
}

func (e *Executor) unregisterControl(jobID string) {
	e.mu.Lock()
	delete(e.controls, jobID)
	e.mu.Unlock()
}

func (e *Executor) getControl(jobID string) (*JobControl, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	jc, ok := e.controls[jobID]
	return jc, ok
}

// CancelJob aborts a running execution (file or database).
func (e *Executor) CancelJob(jobID string) error {
	jc, ok := e.getControl(jobID)
	if !ok {
		return fmt.Errorf("esta tarefa não está em execução")
	}
	jc.Cancel()
	return nil
}

// PauseJob suspends a running file-mirror job between files. Returns an
// error for database jobs — see the package doc comment for why.
func (e *Executor) PauseJob(jobID string) error {
	jc, ok := e.getControl(jobID)
	if !ok {
		return fmt.Errorf("esta tarefa não está em execução")
	}
	if jc.Pause == nil {
		return fmt.Errorf("pausar não é suportado para tarefas de banco de dados (risco de a conexão expirar e corromper o dump) — use cancelar")
	}
	jc.Pause.Pause()
	return nil
}

// ResumeJob un-suspends a previously paused file-mirror job.
func (e *Executor) ResumeJob(jobID string) error {
	jc, ok := e.getControl(jobID)
	if !ok {
		return fmt.Errorf("esta tarefa não está em execução")
	}
	if jc.Pause == nil {
		return fmt.Errorf("esta tarefa não suporta pausa")
	}
	jc.Pause.Resume()
	return nil
}
