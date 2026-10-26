package webui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/cleber/gobackup/internal/backup"
	"github.com/cleber/gobackup/internal/core"
	"github.com/cleber/gobackup/internal/db"
	"github.com/cleber/gobackup/internal/dbjob"
)

// LocalRunner executes jobs directly on this machine — used by the agent's
// own web UI, where "Central" and "local admin" are the same process.
type LocalRunner struct {
	Store    *db.Store
	TenantID uuid.UUID
	AgentID  uuid.UUID
}

func (r *LocalRunner) ListDir(ctx context.Context, path string) ([]DirEntry, error) {
	if path == "" {
		path = "/"
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]DirEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, DirEntry{Name: e.Name(), IsDir: e.IsDir()})
	}
	return out, nil
}

func (r *LocalRunner) CreateFileJob(ctx context.Context, req FileJobRequest) (string, error) {
	switch req.DestType {
	case "", "local", "nas":
	case "ssh":
		if req.SSHHost == "" || req.SSHUser == "" {
			return "", fmt.Errorf("destino ssh requer host e usuário")
		}
	default:
		return "", fmt.Errorf("tipo de destino não suportado: %s", req.DestType)
	}

	jobID, err := r.Store.CreateFileJob(ctx, r.TenantID, r.AgentID, req.Name, req.Source, req.Dest,
		core.BackupType(req.BackupType), req.DestType, req.LinkDest, req.SSHHost, req.SSHPort, req.SSHUser, req.SSHKey, "")
	if err != nil {
		return "", err
	}
	return jobID.String(), nil
}

func (r *LocalRunner) CreateDBJob(ctx context.Context, req DBJobRequest) (string, error) {
	eng := core.DBEngine(req.Engine)
	dbName := req.Database
	if dbName == "" && eng != core.EngineSQLite {
		dbName = core.AllDatabasesSentinel
	}
	compression := req.Compression
	if compression == "" {
		compression = string(core.CompressionGzip)
	}

	connID, err := r.Store.CreateDBConnection(ctx, r.AgentID, req.Name+"-connection", eng, req.Host, req.Port, req.Username, "", dbName, req.SQLitePath)
	if err != nil {
		return "", err
	}
	jobID, err := r.Store.CreateDatabaseJob(ctx, r.TenantID, r.AgentID, connID, req.Name, core.Compression(compression),
		req.DestPath, req.DestType, req.SSHHost, req.SSHPort, req.SSHUser, req.SSHKey, "")
	if err != nil {
		return "", err
	}
	return jobID.String(), nil
}

func (r *LocalRunner) ListJobs(ctx context.Context) ([]JobSummary, error) {
	rows, err := r.Store.ListJobs(ctx, r.AgentID)
	if err != nil {
		return nil, err
	}
	out := make([]JobSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, JobSummary{ID: row.ID.String(), Name: row.Name, Kind: row.Kind})
	}
	return out, nil
}

func (r *LocalRunner) RunJob(ctx context.Context, jobIDStr, password string) (JobOutcome, error) {
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		return JobOutcome{Status: "failed"}, fmt.Errorf("job id inválido")
	}
	kind, err := r.Store.GetJobKind(ctx, jobID)
	if err != nil {
		return JobOutcome{Status: "failed"}, err
	}

	switch kind {
	case "files":
		return r.runFileJob(ctx, jobID)
	case "database":
		return r.runDBJob(ctx, jobID, password)
	default:
		return JobOutcome{Status: "failed"}, fmt.Errorf("tipo de job desconhecido: %s", kind)
	}
}

func (r *LocalRunner) runFileJob(ctx context.Context, jobID uuid.UUID) (JobOutcome, error) {
	d, err := r.Store.GetFileJobDetails(ctx, jobID)
	if err != nil {
		return JobOutcome{Status: "failed"}, err
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
		return JobOutcome{Status: "failed"}, fmt.Errorf("tipo de destino não suportado: %s", d.DestType)
	}

	execID, err := r.Store.StartExecution(ctx, jobID, core.TriggerManual)
	if err != nil {
		return JobOutcome{Status: "failed"}, err
	}

	start := time.Now()
	res, runErr := backup.Run(ctx, d.Source, destination, core.BackupType(d.BackupType), d.LinkDest)
	if runErr != nil {
		_ = r.Store.FinishExecution(ctx, execID, core.ExecFailed, 0, 0, runErr.Error())
		return JobOutcome{Status: "failed", Message: runErr.Error()}, nil
	}
	_ = r.Store.FinishExecution(ctx, execID, core.ExecSuccess, res.BytesTransferred, res.FilesTransferred, "")

	return JobOutcome{
		Status: "success",
		Message: fmt.Sprintf("OK em %s — %d arquivo(s), %d bytes transferidos",
			time.Since(start).Round(time.Millisecond), res.FilesTransferred, res.BytesTransferred),
	}, nil
}

func (r *LocalRunner) runDBJob(ctx context.Context, jobID uuid.UUID, password string) (JobOutcome, error) {
	d, err := r.Store.GetDBJobDetails(ctx, jobID)
	if err != nil {
		return JobOutcome{Status: "failed"}, err
	}

	execID, err := r.Store.StartExecution(ctx, jobID, core.TriggerManual)
	if err != nil {
		return JobOutcome{Status: "failed"}, err
	}

	start := time.Now()
	res, runErr := dbjob.Run(ctx, dbjob.Request{
		Engine: core.DBEngine(d.Engine), Host: d.Host, Port: d.Port, Username: d.Username, Password: password,
		Database: d.Database, SQLitePath: d.SQLitePath, Compression: core.Compression(d.Compression),
		DestType: d.DestType, DestPath: d.DestPath,
		SSHHost: d.SSHHost, SSHPort: d.SSHPort, SSHUser: d.SSHUser, SSHKey: d.SSHKey,
	})
	if runErr != nil {
		_ = r.Store.FinishExecution(ctx, execID, core.ExecFailed, 0, 0, runErr.Error())
		return JobOutcome{Status: "failed", Message: runErr.Error()}, nil
	}
	_ = r.Store.FinishExecution(ctx, execID, core.ExecSuccess, res.BytesWritten, 1, "")

	return JobOutcome{
		Status: "success",
		Message: fmt.Sprintf("OK em %s — gravado em %s (%d bytes)",
			time.Since(start).Round(time.Millisecond), res.DestPath, res.BytesWritten),
	}, nil
}

func (r *LocalRunner) RecentActivity(ctx context.Context, limit int) ([]ActivityItem, error) {
	rows, err := r.Store.ListRecentExecutions(ctx, r.AgentID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ActivityItem, 0, len(rows))
	for _, row := range rows {
		status := row.Status
		desc := fmt.Sprintf("%s (%s)", row.JobName, row.Trigger)
		if row.ErrorMessage != "" {
			desc += ": " + row.ErrorMessage
		}
		when := row.StartedAt
		out = append(out, ActivityItem{When: when, Description: desc, Status: status})
	}
	return out, nil
}
