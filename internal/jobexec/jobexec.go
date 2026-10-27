// Package jobexec is the shared "start a persisted job asynchronously,
// updating a progress.Registry as it runs" engine used both by the local
// web UI (LocalRunner) and by remote run_job commands from the Central
// (poller.Executor) — so a long-running backup never blocks the HTTP
// handler or the heartbeat loop, and both paths report live progress the
// same way.
package jobexec

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/cleber/gobackup/internal/backup"
	"github.com/cleber/gobackup/internal/core"
	"github.com/cleber/gobackup/internal/db"
	"github.com/cleber/gobackup/internal/dbjob"
	"github.com/cleber/gobackup/internal/progress"
)

type Engine struct {
	Store    *db.Store
	Registry *progress.Registry

	mu      sync.Mutex
	running map[string]*jobControl
}

// Start validates the job exists and kicks off its execution in a detached
// goroutine (using context.Background(), NOT the caller's ctx — an HTTP
// request or a single poll tick must not cancel a multi-minute backup).
// It returns immediately; poll progress.Registry (or the HTTP status
// endpoint backed by it) for live updates.
func (e *Engine) Start(ctx context.Context, jobIDStr string, trigger core.ExecutionTrigger, password string) (execIDPlaceholder string, err error) {
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		return "", fmt.Errorf("job id inválido")
	}
	kind, err := e.Store.GetJobKind(ctx, jobID)
	if err != nil {
		return "", err
	}
	switch kind {
	case "files", "database":
	default:
		return "", fmt.Errorf("tipo de job desconhecido: %s", kind)
	}

	e.Registry.Set(jobIDStr, progress.State{Status: "running", Percent: 0})
	go e.run(jobID, kind, trigger, password)
	return "", nil
}

func (e *Engine) run(jobID uuid.UUID, kind string, trigger core.ExecutionTrigger, password string) {
	jobIDStr := jobID.String()
	ctx, setProc := e.register(jobIDStr, kind)
	defer e.unregister(jobIDStr)

	switch kind {
	case "files":
		e.runFile(ctx, jobID, jobIDStr, trigger, setProc)
	case "database":
		e.runDB(ctx, jobID, jobIDStr, trigger, password)
	}
}

func (e *Engine) runFile(ctx context.Context, jobID uuid.UUID, jobIDStr string, trigger core.ExecutionTrigger, onStart func(*os.Process)) {
	d, err := e.Store.GetFileJobDetails(ctx, jobID)
	if err != nil {
		e.fail(ctx, jobIDStr, err)
		return
	}
	var destination backup.Destination
	switch d.DestType {
	case "", "local":
		destination = backup.Local(d.Dest)
	case "nas":
		destination = backup.NAS(d.Dest)
	case "ssh":
		destination = backup.SSH(d.SSHHost, d.SSHPort, d.SSHUser, d.SSHKey, d.Dest)
	default:
		e.fail(ctx, jobIDStr, fmt.Errorf("tipo de destino não suportado: %s", d.DestType))
		return
	}

	execID, err := e.Store.StartExecution(ctx, jobID, trigger)
	if err != nil {
		e.fail(ctx, jobIDStr, err)
		return
	}

	lastUpdate := time.Now()
	res, runErr := backup.Run(ctx, d.Source, destination, core.BackupType(d.BackupType), d.LinkDest, d.DeleteExtraneous, func(pct int, line string) {
		// throttle registry writes to a few per second — plenty for a 1s UI poll
		if time.Since(lastUpdate) < 200*time.Millisecond && pct != 100 {
			return
		}
		lastUpdate = time.Now()
		e.Registry.Set(jobIDStr, progress.State{ExecutionID: execID.String(), Status: "running", Percent: pct, Message: line})
	}, onStart)
	if runErr != nil {
		if ctx.Err() == context.Canceled {
			_ = e.Store.FinishExecution(context.Background(), execID, core.ExecCancelled, 0, 0, "cancelado pelo usuário")
			e.Registry.Set(jobIDStr, progress.State{ExecutionID: execID.String(), Status: "cancelled", Percent: -1, Message: "cancelado pelo usuário"})
			return
		}
		_ = e.Store.FinishExecution(context.Background(), execID, core.ExecFailed, 0, 0, runErr.Error())
		e.Registry.Set(jobIDStr, progress.State{ExecutionID: execID.String(), Status: "failed", Percent: -1, Message: runErr.Error()})
		return
	}
	_ = e.Store.FinishExecution(ctx, execID, core.ExecSuccess, res.BytesTransferred, res.FilesTransferred, "")
	e.Registry.Set(jobIDStr, progress.State{
		ExecutionID: execID.String(), Status: "success", Percent: 100,
		Message: fmt.Sprintf("%d arquivo(s), %d bytes transferidos", res.FilesTransferred, res.BytesTransferred),
	})
}

func (e *Engine) runDB(ctx context.Context, jobID uuid.UUID, jobIDStr string, trigger core.ExecutionTrigger, password string) {
	d, err := e.Store.GetDBJobDetails(ctx, jobID)
	if err != nil {
		e.fail(ctx, jobIDStr, err)
		return
	}

	execID, err := e.Store.StartExecution(ctx, jobID, trigger)
	if err != nil {
		e.fail(ctx, jobIDStr, err)
		return
	}
	// Database dumps don't expose byte-level progress like rsync does, so
	// this stays indeterminate (-1) while running.
	e.Registry.Set(jobIDStr, progress.State{ExecutionID: execID.String(), Status: "running", Percent: -1, Message: "gerando dump..."})

	res, runErr := dbjob.Run(ctx, dbjob.Request{
		Engine: core.DBEngine(d.Engine), Host: d.Host, Port: d.Port, Username: d.Username, Password: password,
		Database: d.Database, SQLitePath: d.SQLitePath, Compression: core.Compression(d.Compression),
		DestType: d.DestType, DestPath: d.DestPath,
		SSHHost: d.SSHHost, SSHPort: d.SSHPort, SSHUser: d.SSHUser, SSHKey: d.SSHKey,
	})
	if runErr != nil {
		if ctx.Err() == context.Canceled {
			_ = e.Store.FinishExecution(context.Background(), execID, core.ExecCancelled, 0, 0, "cancelado pelo usuário")
			e.Registry.Set(jobIDStr, progress.State{ExecutionID: execID.String(), Status: "cancelled", Percent: -1, Message: "cancelado pelo usuário"})
			return
		}
		_ = e.Store.FinishExecution(context.Background(), execID, core.ExecFailed, 0, 0, runErr.Error())
		e.Registry.Set(jobIDStr, progress.State{ExecutionID: execID.String(), Status: "failed", Percent: -1, Message: runErr.Error()})
		return
	}

	retentionMsg := ""
	if d.RetentionDays > 0 {
		deleted, cleanupErr := dbjob.CleanupOld(ctx, dbjob.Request{
			Engine: core.DBEngine(d.Engine), Database: d.Database, SQLitePath: d.SQLitePath,
			DestType: d.DestType, DestPath: d.DestPath,
			SSHHost: d.SSHHost, SSHPort: d.SSHPort, SSHUser: d.SSHUser, SSHKey: d.SSHKey,
		}, d.RetentionDays)
		switch {
		case cleanupErr != nil:
			// Non-fatal: the backup itself already succeeded — a retention
			// cleanup failure shouldn't be reported as the job failing.
			retentionMsg = fmt.Sprintf(" (retenção: falha ao limpar antigos: %v)", cleanupErr)
		case deleted > 0:
			retentionMsg = fmt.Sprintf(" (retenção: %d backup(s) antigo(s) removido(s))", deleted)
		}
	}

	_ = e.Store.FinishExecution(ctx, execID, core.ExecSuccess, res.BytesWritten, 1, "")
	e.Registry.Set(jobIDStr, progress.State{
		ExecutionID: execID.String(), Status: "success", Percent: 100,
		Message: fmt.Sprintf("gravado em %s (%d bytes)%s", res.DestPath, res.BytesWritten, retentionMsg),
	})
}

func (e *Engine) fail(ctx context.Context, jobIDStr string, err error) {
	if ctx.Err() == context.Canceled {
		e.Registry.Set(jobIDStr, progress.State{Status: "cancelled", Percent: -1, Message: "cancelado pelo usuário"})
		return
	}
	e.Registry.Set(jobIDStr, progress.State{Status: "failed", Percent: -1, Message: err.Error()})
}
