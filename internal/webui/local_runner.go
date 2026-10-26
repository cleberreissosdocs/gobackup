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

func (r *LocalRunner) RunFileJob(ctx context.Context, req FileJobRequest) (JobOutcome, error) {
	bt := core.BackupType(req.BackupType)
	var destination backup.Destination
	switch req.DestType {
	case "", "local":
		destination = backup.Local(req.Dest)
	case "nas":
		destination = backup.NAS(req.Dest)
	case "ssh":
		destination = backup.SSH(req.SSHHost, req.SSHPort, req.SSHUser, req.SSHKey, req.Dest)
	default:
		return JobOutcome{Status: "failed"}, fmt.Errorf("unsupported destination type: %s", req.DestType)
	}

	jobID, err := r.Store.CreateFileJob(ctx, r.TenantID, r.AgentID, req.Name, req.Source, req.Dest, bt, "")
	if err != nil {
		return JobOutcome{Status: "failed"}, err
	}
	execID, err := r.Store.StartExecution(ctx, jobID, core.TriggerManual)
	if err != nil {
		return JobOutcome{Status: "failed"}, err
	}

	start := time.Now()
	res, runErr := backup.Run(ctx, req.Source, destination, bt, req.LinkDest)
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

func (r *LocalRunner) RunDBJob(ctx context.Context, req DBJobRequest) (JobOutcome, error) {
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
		return JobOutcome{Status: "failed"}, err
	}
	jobID, err := r.Store.CreateDatabaseJob(ctx, r.TenantID, r.AgentID, connID, req.Name, core.Compression(compression), "")
	if err != nil {
		return JobOutcome{Status: "failed"}, err
	}
	execID, err := r.Store.StartExecution(ctx, jobID, core.TriggerManual)
	if err != nil {
		return JobOutcome{Status: "failed"}, err
	}

	start := time.Now()
	res, runErr := dbjob.Run(ctx, dbjob.Request{
		Engine: eng, Host: req.Host, Port: req.Port, Username: req.Username, Password: req.Password,
		Database: dbName, SQLitePath: req.SQLitePath, Compression: core.Compression(compression),
		DestType: req.DestType, DestPath: req.DestPath,
		SSHHost: req.SSHHost, SSHPort: req.SSHPort, SSHUser: req.SSHUser, SSHKey: req.SSHKey,
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
