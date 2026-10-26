package poller

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

// Executor runs commands received from the Central against the agent's own
// local Postgres and filesystem — the same code path used by `gobackup-agent
// run` for locally-triggered backups, so Central and local administration
// stay in parity. Jobs are always CREATE-then-RUN: "create_job" only
// persists the definition, "run_job" starts an existing one ASYNCHRONOUSLY
// via Engine (so a multi-minute backup never blocks the heartbeat loop).
type Executor struct {
	Store    *db.Store
	TenantID uuid.UUID
	AgentID  uuid.UUID
	Engine   *jobexec.Engine
	Registry *progress.Registry
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
	case "get_job":
		result, err = e.getJob(ctx, cmd.Payload)
	case "update_job":
		result, err = e.updateJob(ctx, cmd.Payload)
	case "list_jobs":
		result, err = e.listJobs(ctx)
	case "run_job":
		result, err = e.runJob(ctx, cmd.Payload)
	case "delete_job":
		result, err = e.deleteJob(ctx, cmd.Payload)
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

func payloadInt(payload map[string]any, key string, def int) int {
	if p, ok := payload[key].(float64); ok { // JSON numbers decode as float64
		return int(p)
	}
	return def
}

func payloadBool(payload map[string]any, key string, def bool) bool {
	if b, ok := payload[key].(bool); ok {
		return b
	}
	return def
}

// createJob persists a new job definition (files or database) WITHOUT
// running it — the operator runs it later (now, repeatedly, or on the
// job's own schedule) via run_job.
func (e *Executor) createJob(ctx context.Context, payload map[string]any) (map[string]any, error) {
	kind, _ := payload["kind"].(string)
	if kind == "" {
		kind = "files" // backward compatibility with the old create_job shape
	}
	name, _ := payload["name"].(string)
	destType, _ := payload["destination_type"].(string)
	linkDest, _ := payload["link_dest"].(string)
	sshHost, _ := payload["ssh_host"].(string)
	sshUser, _ := payload["ssh_user"].(string)
	sshKeyPath, _ := payload["ssh_key_path"].(string)
	sshPort := payloadInt(payload, "ssh_port", 22)
	cron, _ := payload["cron"].(string)

	switch kind {
	case "files":
		source, _ := payload["source_path"].(string)
		dest, _ := payload["destination_path"].(string)
		backupType, _ := payload["backup_type"].(string)
		if name == "" || source == "" || dest == "" {
			return nil, fmt.Errorf("create_job (files) requires name, source_path, destination_path")
		}
		if backupType == "" {
			backupType = string(core.BackupFull)
		}
		jobID, err := e.Store.CreateFileJob(ctx, e.TenantID, e.AgentID, name, source, dest,
			core.BackupType(backupType), destType, linkDest, sshHost, sshPort, sshUser, sshKeyPath, cron)
		if err != nil {
			return nil, err
		}
		return map[string]any{"job_id": jobID.String(), "kind": "files"}, nil

	case "database":
		engine, _ := payload["engine"].(string)
		host, _ := payload["host"].(string)
		username, _ := payload["username"].(string)
		database, _ := payload["database"].(string)
		sqlitePath, _ := payload["sqlite_path"].(string)
		compression, _ := payload["compression"].(string)
		destPath, _ := payload["destination_path"].(string)
		port := payloadInt(payload, "port", 0)

		if name == "" || engine == "" || destPath == "" {
			return nil, fmt.Errorf("create_job (database) requires name, engine, destination_path")
		}
		eng := core.DBEngine(engine)
		if database == "" && eng != core.EngineSQLite {
			database = core.AllDatabasesSentinel
		}
		if compression == "" {
			compression = string(core.CompressionGzip)
		}

		connID, err := e.Store.CreateDBConnection(ctx, e.AgentID, name+"-connection", eng, host, port, username, "", database, sqlitePath)
		if err != nil {
			return nil, err
		}
		jobID, err := e.Store.CreateDatabaseJob(ctx, e.TenantID, e.AgentID, connID, name, core.Compression(compression),
			destPath, destType, sshHost, sshPort, sshUser, sshKeyPath, cron)
		if err != nil {
			return nil, err
		}
		return map[string]any{"job_id": jobID.String(), "kind": "database"}, nil

	default:
		return nil, fmt.Errorf("unsupported job kind: %s", kind)
	}
}

// deleteJob removes a job definition (and its execution history, via FK
// cascade). Does not touch files already written to past destinations.
func (e *Executor) deleteJob(ctx context.Context, payload map[string]any) (map[string]any, error) {
	jobIDStr, _ := payload["job_id"].(string)
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		return nil, fmt.Errorf("job_id inválido")
	}
	if err := e.Store.DeleteJob(ctx, jobID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

// getJob returns a persisted job's full definition (minus any password),
// for the Central's "Editar" form to prefill.
func (e *Executor) getJob(ctx context.Context, payload map[string]any) (map[string]any, error) {
	jobIDStr, _ := payload["job_id"].(string)
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		return nil, fmt.Errorf("job_id inválido")
	}
	kind, err := e.Store.GetJobKind(ctx, jobID)
	if err != nil {
		return nil, err
	}

	switch kind {
	case "files":
		d, err := e.Store.GetFileJobDetails(ctx, jobID)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"kind": "files", "name": d.Name, "source_path": d.Source, "destination_path": d.Dest,
			"backup_type": d.BackupType, "link_dest": d.LinkDest, "destination_type": d.DestType,
			"ssh_host": d.SSHHost, "ssh_port": d.SSHPort, "ssh_user": d.SSHUser, "ssh_key_path": d.SSHKey,
			"cron": d.Cron, "enabled": d.Enabled,
		}, nil
	case "database":
		d, err := e.Store.GetDBJobDetails(ctx, jobID)
		if err != nil {
			return nil, err
		}
		database := d.Database
		if database == core.AllDatabasesSentinel {
			database = ""
		}
		return map[string]any{
			"kind": "database", "name": d.Name, "engine": d.Engine, "host": d.Host, "port": d.Port, "username": d.Username,
			"database": database, "sqlite_path": d.SQLitePath, "compression": d.Compression,
			"destination_path": d.DestPath, "destination_type": d.DestType,
			"ssh_host": d.SSHHost, "ssh_port": d.SSHPort, "ssh_user": d.SSHUser, "ssh_key_path": d.SSHKey,
			"cron": d.Cron, "enabled": d.Enabled,
		}, nil
	default:
		return nil, fmt.Errorf("unknown job kind: %s", kind)
	}
}

// updateJob overwrites an existing job's definition in place (the
// "Editar" form's save action).
func (e *Executor) updateJob(ctx context.Context, payload map[string]any) (map[string]any, error) {
	jobIDStr, _ := payload["job_id"].(string)
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		return nil, fmt.Errorf("job_id inválido")
	}
	kind, _ := payload["kind"].(string)
	name, _ := payload["name"].(string)
	destType, _ := payload["destination_type"].(string)
	sshHost, _ := payload["ssh_host"].(string)
	sshUser, _ := payload["ssh_user"].(string)
	sshKeyPath, _ := payload["ssh_key_path"].(string)
	sshPort := payloadInt(payload, "ssh_port", 22)
	cron, _ := payload["cron"].(string)
	enabled := payloadBool(payload, "enabled", true)

	switch kind {
	case "files":
		source, _ := payload["source_path"].(string)
		dest, _ := payload["destination_path"].(string)
		backupType, _ := payload["backup_type"].(string)
		linkDest, _ := payload["link_dest"].(string)
		if err := e.Store.UpdateFileJobByID(ctx, jobID, name, source, dest, core.BackupType(backupType),
			destType, linkDest, sshHost, sshPort, sshUser, sshKeyPath, cron, enabled); err != nil {
			return nil, err
		}
		return map[string]any{"job_id": jobIDStr}, nil

	case "database":
		engine, _ := payload["engine"].(string)
		host, _ := payload["host"].(string)
		username, _ := payload["username"].(string)
		database, _ := payload["database"].(string)
		sqlitePath, _ := payload["sqlite_path"].(string)
		compression, _ := payload["compression"].(string)
		destPath, _ := payload["destination_path"].(string)
		port := payloadInt(payload, "port", 0)
		eng := core.DBEngine(engine)
		if database == "" && eng != core.EngineSQLite {
			database = core.AllDatabasesSentinel
		}
		if err := e.Store.UpdateDatabaseJobByID(ctx, jobID, name, eng, host, port, username, database, sqlitePath,
			core.Compression(compression), destPath, destType, sshHost, sshPort, sshUser, sshKeyPath, cron, enabled); err != nil {
			return nil, err
		}
		return map[string]any{"job_id": jobIDStr}, nil

	default:
		return nil, fmt.Errorf("unsupported job kind: %s", kind)
	}
}

// listJobs reports every persisted job definition for this agent, for the
// Central's "Tarefas" list (same data as the agent's own local UI).
func (e *Executor) listJobs(ctx context.Context) (map[string]any, error) {
	rows, err := e.Store.ListJobs(ctx, e.AgentID)
	if err != nil {
		return nil, err
	}
	type jobEntry struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Kind       string `json:"kind"`
		Cron       string `json:"cron"`
		Enabled    bool   `json:"enabled"`
		LastStatus string `json:"last_status"`
	}
	list := make([]jobEntry, 0, len(rows))
	for _, row := range rows {
		list = append(list, jobEntry{ID: row.ID.String(), Name: row.Name, Kind: row.Kind, Cron: row.Cron, Enabled: row.Enabled, LastStatus: row.LastStatus})
	}
	return map[string]any{"jobs": list}, nil
}

// runJob STARTS an already-persisted job asynchronously via Engine and
// acks immediately — it does NOT wait for the backup to finish, so a
// multi-minute transfer never blocks this heartbeat tick (or the Central's
// HTTP handler on the other end, which doesn't wait either).
func (e *Executor) runJob(ctx context.Context, payload map[string]any) (map[string]any, error) {
	jobIDStr, _ := payload["job_id"].(string)
	password, _ := payload["password"].(string)
	if _, err := e.Engine.Start(ctx, jobIDStr, core.TriggerRemoteCentral, password); err != nil {
		return nil, err
	}
	return map[string]any{"job_id": jobIDStr, "status": "started"}, nil
}
