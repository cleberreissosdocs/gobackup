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
// back into) for the agent to pick it up and report a result. Long-running
// actions (RunJob) don't wait at all — progress comes from a cache the
// agent refreshes on every heartbeat (JobStatus), never from blocking here.
type CentralRunner struct {
	Store   *db.Store
	AgentID uuid.UUID

	// WaitTimeout bounds how long the HTTP handler blocks waiting for the
	// agent's next poll cycle to deliver+execute a quick command (create,
	// get, update, list — NOT run, which never blocks). Defaults to 5s if
	// zero (comfortably longer than the agent's 1s poll interval).
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

func fileJobPayload(req FileJobRequest) map[string]any {
	return map[string]any{
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
		"cron":             req.Cron,
		"enabled":          req.Enabled,
	}
}

func dbJobPayload(req DBJobRequest) map[string]any {
	return map[string]any{
		"name":             req.Name,
		"engine":           req.Engine,
		"host":             req.Host,
		"port":             req.Port,
		"username":         req.Username,
		"database":         req.Database,
		"sqlite_path":      req.SQLitePath,
		"compression":      req.Compression,
		"destination_path": req.DestPath,
		"destination_type": req.DestType,
		"ssh_host":         req.SSHHost,
		"ssh_port":         req.SSHPort,
		"ssh_user":         req.SSHUser,
		"ssh_key_path":     req.SSHKey,
		"cron":             req.Cron,
		"enabled":          req.Enabled,
	}
}

func (r *CentralRunner) CreateFileJob(ctx context.Context, req FileJobRequest) (string, error) {
	payload := fileJobPayload(req)
	payload["kind"] = "files"
	outcome, err := r.enqueueAndWait(ctx, "create_job", payload)
	if err != nil {
		return "", err
	}
	if outcome.Status != "success" {
		return "", fmt.Errorf("%s", outcome.Message)
	}
	_, result, err := r.lastCommandResult(ctx, "create_job")
	if err != nil {
		return "", err
	}
	jobID, _ := result["job_id"].(string)
	return jobID, nil
}

func (r *CentralRunner) CreateDBJob(ctx context.Context, req DBJobRequest) (string, error) {
	payload := dbJobPayload(req)
	payload["kind"] = "database"
	outcome, err := r.enqueueAndWait(ctx, "create_job", payload)
	if err != nil {
		return "", err
	}
	if outcome.Status != "success" {
		return "", fmt.Errorf("%s", outcome.Message)
	}
	_, result, err := r.lastCommandResult(ctx, "create_job")
	if err != nil {
		return "", err
	}
	jobID, _ := result["job_id"].(string)
	return jobID, nil
}

func strField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func intField(m map[string]any, key string) int {
	if f, ok := m[key].(float64); ok {
		return int(f)
	}
	return 0
}

func boolField(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func (r *CentralRunner) GetFileJob(ctx context.Context, jobID string) (FileJobRequest, error) {
	outcome, err := r.enqueueAndWait(ctx, "get_job", map[string]any{"job_id": jobID})
	if err != nil {
		return FileJobRequest{}, err
	}
	if outcome.Status != "success" {
		return FileJobRequest{}, fmt.Errorf("%s", outcome.Message)
	}
	_, m, err := r.lastCommandResult(ctx, "get_job")
	if err != nil {
		return FileJobRequest{}, err
	}
	return FileJobRequest{
		Name: strField(m, "name"), Source: strField(m, "source_path"), Dest: strField(m, "destination_path"),
		BackupType: strField(m, "backup_type"), LinkDest: strField(m, "link_dest"), DestType: strField(m, "destination_type"),
		SSHHost: strField(m, "ssh_host"), SSHPort: intField(m, "ssh_port"), SSHUser: strField(m, "ssh_user"), SSHKey: strField(m, "ssh_key_path"),
		Cron: strField(m, "cron"), Enabled: boolField(m, "enabled"),
	}, nil
}

func (r *CentralRunner) GetDBJob(ctx context.Context, jobID string) (DBJobRequest, error) {
	outcome, err := r.enqueueAndWait(ctx, "get_job", map[string]any{"job_id": jobID})
	if err != nil {
		return DBJobRequest{}, err
	}
	if outcome.Status != "success" {
		return DBJobRequest{}, fmt.Errorf("%s", outcome.Message)
	}
	_, m, err := r.lastCommandResult(ctx, "get_job")
	if err != nil {
		return DBJobRequest{}, err
	}
	return DBJobRequest{
		Name: strField(m, "name"), Engine: strField(m, "engine"), Host: strField(m, "host"), Port: intField(m, "port"),
		Username: strField(m, "username"), Database: strField(m, "database"), SQLitePath: strField(m, "sqlite_path"),
		Compression: strField(m, "compression"), DestPath: strField(m, "destination_path"), DestType: strField(m, "destination_type"),
		SSHHost: strField(m, "ssh_host"), SSHPort: intField(m, "ssh_port"), SSHUser: strField(m, "ssh_user"), SSHKey: strField(m, "ssh_key_path"),
		Cron: strField(m, "cron"), Enabled: boolField(m, "enabled"),
	}, nil
}

func (r *CentralRunner) UpdateFileJob(ctx context.Context, jobID string, req FileJobRequest) error {
	payload := fileJobPayload(req)
	payload["kind"] = "files"
	payload["job_id"] = jobID
	outcome, err := r.enqueueAndWait(ctx, "update_job", payload)
	if err != nil {
		return err
	}
	if outcome.Status != "success" {
		return fmt.Errorf("%s", outcome.Message)
	}
	return nil
}

func (r *CentralRunner) UpdateDBJob(ctx context.Context, jobID string, req DBJobRequest) error {
	payload := dbJobPayload(req)
	payload["kind"] = "database"
	payload["job_id"] = jobID
	outcome, err := r.enqueueAndWait(ctx, "update_job", payload)
	if err != nil {
		return err
	}
	if outcome.Status != "success" {
		return fmt.Errorf("%s", outcome.Message)
	}
	return nil
}

func (r *CentralRunner) ListJobs(ctx context.Context) ([]JobSummary, error) {
	outcome, err := r.enqueueAndWait(ctx, "list_jobs", map[string]any{})
	if err != nil {
		return nil, err
	}
	if outcome.Status != "success" {
		return nil, fmt.Errorf("%s", outcome.Message)
	}
	_, result, err := r.lastCommandResult(ctx, "list_jobs")
	if err != nil {
		return nil, err
	}
	jobsRaw, _ := result["jobs"].([]any)
	out := make([]JobSummary, 0, len(jobsRaw))
	for _, j := range jobsRaw {
		m, ok := j.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, JobSummary{
			ID: strField(m, "id"), Name: strField(m, "name"), Kind: strField(m, "kind"),
			Cron: strField(m, "cron"), Enabled: boolField(m, "enabled"), Status: strField(m, "last_status"),
		})
	}
	return out, nil
}

func (r *CentralRunner) DeleteJob(ctx context.Context, jobID string) error {
	outcome, err := r.enqueueAndWait(ctx, "delete_job", map[string]any{"job_id": jobID})
	if err != nil {
		return err
	}
	if outcome.Status != "success" {
		return fmt.Errorf("%s", outcome.Message)
	}
	return nil
}

// RunJob fires the run_job command and returns immediately — it does NOT
// wait for completion. Progress is reported via JobStatus, refreshed by the
// agent's next heartbeat(s), same mechanism as the jobs list.
func (r *CentralRunner) RunJob(ctx context.Context, jobID, password string) (JobOutcome, error) {
	if _, err := r.Store.EnqueueCommand(ctx, r.AgentID, "run_job", map[string]any{"job_id": jobID, "password": password}); err != nil {
		return JobOutcome{Status: "failed", Message: err.Error()}, nil
	}
	return JobOutcome{Status: "started", Message: "Comando enviado ao agente."}, nil
}

func (r *CentralRunner) JobStatus(ctx context.Context, jobID string) (ProgressInfo, error) {
	status, percent, message, found, err := r.Store.GetAgentJobStatus(ctx, r.AgentID, jobID)
	if err != nil {
		return ProgressInfo{}, err
	}
	if !found {
		return ProgressInfo{Status: "idle"}, nil
	}
	return ProgressInfo{Status: status, Percent: percent, Message: message}, nil
}

func (r *CentralRunner) RecentActivity(ctx context.Context, limit int) ([]ActivityItem, error) {
	rows, err := r.Store.ListRecentCommands(ctx, r.AgentID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ActivityItem, 0, len(rows))
	for _, row := range rows {
		desc := commandLabel(row.Type)
		if row.Status == "failed" {
			if errMsg, _ := row.Result["error"].(string); errMsg != "" {
				desc += ": " + humanizeErrorReason(errMsg)
			}
		}
		out = append(out, ActivityItem{
			When:        row.CreatedAt,
			Description: desc,
			Status:      row.Status,
		})
	}
	return out, nil
}
