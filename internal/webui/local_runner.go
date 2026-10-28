package webui

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"

	"github.com/cleber/gobackup/internal/core"
	"github.com/cleber/gobackup/internal/db"
	"github.com/cleber/gobackup/internal/jobexec"
	"github.com/cleber/gobackup/internal/progress"
)

// LocalRunner executes jobs directly on this machine — used by the agent's
// own web UI, where "Central" and "local admin" are the same process.
type LocalRunner struct {
	Store    *db.Store
	TenantID uuid.UUID
	AgentID  uuid.UUID
	Engine   *jobexec.Engine // shared with the remote-command executor and the scheduler
	Registry *progress.Registry
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

func validateFileDest(req FileJobRequest) error {
	switch req.DestType {
	case "", "local", "nas":
		return nil
	case "ssh":
		if req.SSHHost == "" || req.SSHUser == "" {
			return fmt.Errorf("destino ssh requer host e usuário")
		}
		return nil
	default:
		return fmt.Errorf("tipo de destino não suportado: %s", req.DestType)
	}
}

func (r *LocalRunner) CreateFileJob(ctx context.Context, req FileJobRequest) (string, error) {
	if err := validateFileDest(req); err != nil {
		return "", err
	}
	jobID, err := r.Store.CreateFileJob(ctx, r.TenantID, r.AgentID, req.Name, req.Source, req.Dest,
		core.BackupType(req.BackupType), req.DestType, req.LinkDest, req.DeleteExtraneous, req.SSHHost, req.SSHPort, req.SSHUser, req.SSHKey, req.Cron)
	if err != nil {
		return "", err
	}
	return jobID.String(), nil
}

func dbDefaults(req DBJobRequest) (dbName, compression string) {
	eng := core.DBEngine(req.Engine)
	dbName = req.Database
	if dbName == "" && eng != core.EngineSQLite {
		dbName = core.AllDatabasesSentinel
	}
	compression = req.Compression
	if compression == "" {
		compression = string(core.CompressionGzip)
	}
	return dbName, compression
}

func (r *LocalRunner) CreateDBJob(ctx context.Context, req DBJobRequest) (string, error) {
	dbName, compression := dbDefaults(req)
	eng := core.DBEngine(req.Engine)

	connID, err := r.Store.CreateDBConnection(ctx, r.AgentID, req.Name+"-connection", eng, req.Host, req.Port, req.Username, "", dbName, req.SQLitePath)
	if err != nil {
		return "", err
	}
	jobID, err := r.Store.CreateDatabaseJob(ctx, r.TenantID, r.AgentID, connID, req.Name, core.Compression(compression),
		req.DestPath, req.DestType, req.SSHHost, req.SSHPort, req.SSHUser, req.SSHKey, req.Cron, req.RetentionDays)
	if err != nil {
		return "", err
	}
	return jobID.String(), nil
}

func (r *LocalRunner) GetFileJob(ctx context.Context, jobIDStr string) (FileJobRequest, error) {
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		return FileJobRequest{}, fmt.Errorf("job id inválido")
	}
	d, err := r.Store.GetFileJobDetails(ctx, jobID)
	if err != nil {
		return FileJobRequest{}, err
	}
	return FileJobRequest{
		Name: d.Name, Source: d.Source, Dest: d.Dest, BackupType: d.BackupType, LinkDest: d.LinkDest, DeleteExtraneous: d.DeleteExtraneous,
		DestType: d.DestType, SSHHost: d.SSHHost, SSHPort: d.SSHPort, SSHUser: d.SSHUser, SSHKey: d.SSHKey,
		Cron: d.Cron, Enabled: d.Enabled,
	}, nil
}

func (r *LocalRunner) GetDBJob(ctx context.Context, jobIDStr string) (DBJobRequest, error) {
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		return DBJobRequest{}, fmt.Errorf("job id inválido")
	}
	d, err := r.Store.GetDBJobDetails(ctx, jobID)
	if err != nil {
		return DBJobRequest{}, err
	}
	database := d.Database
	if database == core.AllDatabasesSentinel {
		database = ""
	}
	return DBJobRequest{
		Name: d.Name, Engine: d.Engine, Host: d.Host, Port: d.Port, Username: d.Username,
		Database: database, SQLitePath: d.SQLitePath, Compression: d.Compression,
		DestPath: d.DestPath, DestType: d.DestType, SSHHost: d.SSHHost, SSHPort: d.SSHPort, SSHUser: d.SSHUser, SSHKey: d.SSHKey,
		RetentionDays: d.RetentionDays, Cron: d.Cron, Enabled: d.Enabled,
	}, nil
}

func (r *LocalRunner) UpdateFileJob(ctx context.Context, jobIDStr string, req FileJobRequest) error {
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		return fmt.Errorf("job id inválido")
	}
	if err := validateFileDest(req); err != nil {
		return err
	}
	return r.Store.UpdateFileJobByID(ctx, jobID, req.Name, req.Source, req.Dest, core.BackupType(req.BackupType),
		req.DestType, req.LinkDest, req.DeleteExtraneous, req.SSHHost, req.SSHPort, req.SSHUser, req.SSHKey, req.Cron, req.Enabled)
}

func (r *LocalRunner) UpdateDBJob(ctx context.Context, jobIDStr string, req DBJobRequest) error {
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		return fmt.Errorf("job id inválido")
	}
	dbName, compression := dbDefaults(req)
	return r.Store.UpdateDatabaseJobByID(ctx, jobID, req.Name, core.DBEngine(req.Engine), req.Host, req.Port, req.Username,
		dbName, req.SQLitePath, core.Compression(compression), req.DestPath, req.DestType, req.SSHHost, req.SSHPort, req.SSHUser, req.SSHKey,
		req.Cron, req.RetentionDays, req.Enabled)
}

func (r *LocalRunner) ListJobs(ctx context.Context) ([]JobSummary, error) {
	rows, err := r.Store.ListJobs(ctx, r.AgentID)
	if err != nil {
		return nil, err
	}
	out := make([]JobSummary, 0, len(rows))
	for _, row := range rows {
		s := JobSummary{ID: row.ID.String(), Name: row.Name, Kind: row.Kind, Cron: row.Cron, Enabled: row.Enabled, Status: row.LastStatus}
		if s.Status == "running" {
			if live, ok := r.Registry.Get(s.ID); ok {
				s.Percent = live.Percent
			}
		}
		out = append(out, s)
	}
	return out, nil
}

func (r *LocalRunner) DeleteJob(ctx context.Context, jobIDStr string) error {
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		return fmt.Errorf("job id inválido")
	}
	return r.Store.DeleteJob(ctx, jobID)
}

func (r *LocalRunner) RunJob(ctx context.Context, jobID, password string) (JobOutcome, error) {
	if _, err := r.Engine.Start(ctx, jobID, core.TriggerManual, password); err != nil {
		return JobOutcome{Status: "failed", Message: err.Error()}, nil
	}
	return JobOutcome{Status: "started", Message: "Execução iniciada."}, nil
}

func (r *LocalRunner) JobStatus(ctx context.Context, jobIDStr string) (ProgressInfo, error) {
	if s, ok := r.Registry.Get(jobIDStr); ok {
		return ProgressInfo{Status: s.Status, Percent: s.Percent, Message: s.Message}, nil
	}
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		return ProgressInfo{Status: "idle"}, nil
	}
	last, _ := r.Store.GetLatestExecution(ctx, jobID)
	if !last.Found {
		return ProgressInfo{Status: "idle"}, nil
	}
	status := "idle"
	switch last.Status {
	case "success":
		status = "success"
	case "failed":
		status = "failed"
	case "running":
		status = "running"
	case "cancelled":
		status = "cancelled"
	}
	pct := -1
	if status == "success" {
		pct = 100
	}
	return ProgressInfo{Status: status, Percent: pct, Message: last.ErrorMessage}, nil
}

// CancelJob/PauseJob/ResumeJob delegate straight to the shared Engine —
// see internal/jobexec/control.go for what each actually does and why
// Pause/Resume only works for file jobs.
func (r *LocalRunner) CancelJob(ctx context.Context, jobIDStr string) error {
	return r.Engine.Cancel(jobIDStr)
}

func (r *LocalRunner) PauseJob(ctx context.Context, jobIDStr string) error {
	return r.Engine.Pause(jobIDStr)
}

func (r *LocalRunner) ResumeJob(ctx context.Context, jobIDStr string) error {
	return r.Engine.Resume(jobIDStr)
}

func (r *LocalRunner) RecentActivity(ctx context.Context, limit int) ([]ActivityItem, error) {
	rows, err := r.Store.ListRecentExecutions(ctx, r.AgentID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ActivityItem, 0, len(rows))
	for _, row := range rows {
		desc := humanizeExecution(row.JobName, row.Status, row.Trigger, row.ErrorMessage)
		out = append(out, ActivityItem{When: row.StartedAt, Description: desc, Status: row.Status})
	}
	return out, nil
}
