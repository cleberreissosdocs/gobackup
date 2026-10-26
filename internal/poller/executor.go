package poller

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"

	"github.com/cleber/gobackup/internal/backup"
	"github.com/cleber/gobackup/internal/core"
	"github.com/cleber/gobackup/internal/db"
)

// Executor runs commands received from the Central against the agent's own
// local Postgres and filesystem — the same code path used by `gobackup-agent
// run` for locally-triggered backups, so Central and local administration
// stay in parity.
type Executor struct {
	Store    *db.Store
	TenantID uuid.UUID
	AgentID  uuid.UUID
}

// Execute runs a single queued command and returns the CommandResult to be
// reported back to the Central on the next poll.
func (e *Executor) Execute(ctx context.Context, cmd QueuedCommand) CommandResult {
	var (
		result map[string]any
		err    error
	)

	switch cmd.Type {
	case "list_dir":
		result, err = e.listDir(cmd.Payload)
	case "create_job":
		result, err = e.createJob(ctx, cmd.Payload)
	case "start_job":
		result, err = e.startJob(ctx, cmd.Payload)
	default:
		err = fmt.Errorf("unsupported command type: %s", cmd.Type)
	}

	if err != nil {
		return CommandResult{CommandID: cmd.ID, Status: "failed", Result: map[string]any{"error": err.Error()}}
	}
	return CommandResult{CommandID: cmd.ID, Status: "acked", Result: result}
}

func (e *Executor) listDir(payload map[string]any) (map[string]any, error) {
	path, _ := payload["path"].(string)
	if path == "" {
		path = "/"
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("list_dir %q: %w", path, err)
	}

	type entry struct {
		Name  string `json:"name"`
		IsDir bool   `json:"is_dir"`
	}
	list := make([]entry, 0, len(entries))
	for _, de := range entries {
		list = append(list, entry{Name: de.Name(), IsDir: de.IsDir()})
	}
	return map[string]any{"path": path, "entries": list}, nil
}

func (e *Executor) createJob(ctx context.Context, payload map[string]any) (map[string]any, error) {
	name, _ := payload["name"].(string)
	source, _ := payload["source_path"].(string)
	dest, _ := payload["destination_path"].(string)
	backupType, _ := payload["backup_type"].(string)
	cron, _ := payload["cron"].(string)

	if name == "" || source == "" || dest == "" {
		return nil, fmt.Errorf("create_job requires name, source_path, destination_path")
	}
	if backupType == "" {
		backupType = string(core.BackupFull)
	}

	jobID, err := e.Store.CreateFileJob(ctx, e.TenantID, e.AgentID, name, source, dest, core.BackupType(backupType), cron)
	if err != nil {
		return nil, err
	}
	return map[string]any{"job_id": jobID.String()}, nil
}

func (e *Executor) startJob(ctx context.Context, payload map[string]any) (map[string]any, error) {
	name, _ := payload["name"].(string)
	source, _ := payload["source_path"].(string)
	dest, _ := payload["destination_path"].(string)
	backupType, _ := payload["backup_type"].(string)
	linkDest, _ := payload["link_dest"].(string)

	if source == "" || dest == "" {
		return nil, fmt.Errorf("start_job requires source_path and destination_path")
	}
	if backupType == "" {
		backupType = string(core.BackupFull)
	}
	if name == "" {
		name = fmt.Sprintf("remote-%s", source)
	}
	bt := core.BackupType(backupType)

	jobID, err := e.Store.CreateFileJob(ctx, e.TenantID, e.AgentID, name, source, dest, bt, "")
	if err != nil {
		return nil, err
	}

	execID, err := e.Store.StartExecution(ctx, jobID, core.TriggerRemoteCentral)
	if err != nil {
		return nil, err
	}

	res, runErr := backup.Run(ctx, source, dest, bt, linkDest)
	if runErr != nil {
		_ = e.Store.FinishExecution(ctx, execID, core.ExecFailed, 0, 0, runErr.Error())
		return nil, runErr
	}

	if err := e.Store.FinishExecution(ctx, execID, core.ExecSuccess, res.BytesTransferred, res.FilesTransferred, ""); err != nil {
		return nil, err
	}

	return map[string]any{
		"job_id":            jobID.String(),
		"execution_id":      execID.String(),
		"bytes_transferred": res.BytesTransferred,
		"files_transferred": res.FilesTransferred,
	}, nil
}
