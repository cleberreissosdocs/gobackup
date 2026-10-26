package webui

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/cleber/gobackup/internal/db"
)

// CentralRunner executes jobs on a remote agent via the command queue:
// enqueue, then wait (short-poll the DB, same storage the agent reports
// back into) for the agent to pick it up and report a result.
type CentralRunner struct {
	Store   *db.Store
	AgentID uuid.UUID

	// WaitTimeout bounds how long the HTTP handler blocks waiting for the
	// agent's next poll cycle to deliver+execute the command. Defaults to
	// 5s if zero (comfortably longer than the agent's 1s poll interval).
	WaitTimeout time.Duration
}

func (r *CentralRunner) timeout() time.Duration {
	if r.WaitTimeout > 0 {
		return r.WaitTimeout
	}
	return 5 * time.Second
}

func (r *CentralRunner) enqueueAndWait(ctx context.Context, cmdType string, payload map[string]any) (JobOutcome, error) {
	cmdID, err := r.Store.EnqueueCommand(ctx, r.AgentID, cmdType, payload)
	if err != nil {
		return JobOutcome{Status: "failed"}, err
	}

	deadline := time.Now().Add(r.timeout())
	for time.Now().Before(deadline) {
		status, result, err := r.Store.GetCommand(ctx, cmdID)
		if err == nil {
			switch status {
			case "acked":
				return JobOutcome{Status: "success", Message: formatResult(result)}, nil
			case "failed":
				return JobOutcome{Status: "failed", Message: formatResult(result)}, nil
			}
		}
		time.Sleep(1 * time.Second)
	}
	return JobOutcome{
		Status:  "pending",
		Message: fmt.Sprintf("comando %s enviado; o agente ainda não confirmou (verifique a atividade recente em instantes)", cmdID),
	}, nil
}

func formatResult(result map[string]any) string {
	if result == nil {
		return ""
	}
	if errMsg, ok := result["error"].(string); ok && errMsg != "" {
		return errMsg
	}
	out := ""
	for k, v := range result {
		out += fmt.Sprintf("%s=%v ", k, v)
	}
	return out
}

func (r *CentralRunner) ListDir(ctx context.Context, path string) ([]DirEntry, error) {
	outcome, err := r.enqueueAndWait(ctx, "list_dir", map[string]any{"path": path})
	if err != nil {
		return nil, err
	}
	if outcome.Status != "success" {
		return nil, fmt.Errorf("%s", outcome.Message)
	}
	_, result, err := r.lastCommandResult(ctx, "list_dir")
	if err != nil {
		return nil, err
	}
	entriesRaw, _ := result["entries"].([]any)
	out := make([]DirEntry, 0, len(entriesRaw))
	for _, e := range entriesRaw {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		isDir, _ := m["is_dir"].(bool)
		out = append(out, DirEntry{Name: name, IsDir: isDir})
	}
	return out, nil
}

// lastCommandResult is a small helper to re-fetch the freshest result for a
// command type right after enqueueAndWait confirms success (keeps
// enqueueAndWait's return type uniform across callers).
func (r *CentralRunner) lastCommandResult(ctx context.Context, cmdType string) (string, map[string]any, error) {
	rows, err := r.Store.ListRecentCommands(ctx, r.AgentID, 5)
	if err != nil {
		return "", nil, err
	}
	for _, row := range rows {
		if row.Type == cmdType && row.Status == "acked" {
			return row.Status, row.Result, nil
		}
	}
	return "", map[string]any{}, nil
}

func (r *CentralRunner) RunFileJob(ctx context.Context, req FileJobRequest) (JobOutcome, error) {
	payload := map[string]any{
		"name":             req.Name,
		"source_path":      req.Source,
		"destination_path": req.Dest,
		"backup_type":      req.BackupType,
		"link_dest":        req.LinkDest,
		"destination_type": req.DestType,
		"ssh_host":         req.SSHHost,
		"ssh_port":         req.SSHPort,
		"ssh_user":         req.SSHUser,
		"ssh_key_path":     req.SSHKey,
	}
	return r.enqueueAndWait(ctx, "start_job", payload)
}

func (r *CentralRunner) RunDBJob(ctx context.Context, req DBJobRequest) (JobOutcome, error) {
	payload := map[string]any{
		"name":             req.Name,
		"engine":           req.Engine,
		"host":             req.Host,
		"port":             req.Port,
		"username":         req.Username,
		"password":         req.Password,
		"database":         req.Database,
		"sqlite_path":      req.SQLitePath,
		"compression":      req.Compression,
		"destination_path": req.DestPath,
		"destination_type": req.DestType,
		"ssh_host":         req.SSHHost,
		"ssh_port":         req.SSHPort,
		"ssh_user":         req.SSHUser,
		"ssh_key_path":     req.SSHKey,
	}
	return r.enqueueAndWait(ctx, "start_db_job", payload)
}

func (r *CentralRunner) RecentActivity(ctx context.Context, limit int) ([]ActivityItem, error) {
	rows, err := r.Store.ListRecentCommands(ctx, r.AgentID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ActivityItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, ActivityItem{
			When:        row.CreatedAt,
			Description: fmt.Sprintf("%s: %s", row.Type, formatResult(row.Result)),
			Status:      row.Status,
		})
	}
	return out, nil
}
