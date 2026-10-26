package poller

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"

	"github.com/cleber/gobackup/internal/backup"
	"github.com/cleber/gobackup/internal/core"
	"github.com/cleber/gobackup/internal/db"
	"github.com/cleber/gobackup/internal/dbjob"
)

// Executor runs commands received from the Central against the agent's own
// local Postgres and filesystem — the same code path used by `gobackup-agent
// run` for locally-triggered backups, so Central and local administration
// stay in parity. Jobs are always CREATE-then-RUN: "create_job" only
// persists the definition, "run_job" executes an existing one by ID.
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
	case "list_jobs":
		result, err = e.listJobs(ctx)
	case "run_job":
		result, err = e.runJob(ctx, cmd.Payload)
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

// createJob persists a new job definition (files or database) WITHOUT
// running it — the operator runs it later (now or repeatedly) via run_job.
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
			core.BackupType(backupType), destType, linkDest, sshHost, sshPort, sshUser, sshKeyPath, "")
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
			destPath, destType, sshHost, sshPort, sshUser, sshKeyPath, "")
		if err != nil {
			return nil, err
		}
		return map[string]any{"job_id": jobID.String(), "kind": "database"}, nil

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
		ID   string `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	list := make([]jobEntry, 0, len(rows))
	for _, row := range rows {
		list = append(list, jobEntry{ID: row.ID.String(), Name: row.Name, Kind: row.Kind})
	}
	return map[string]any{"jobs": list}, nil
}

// runJob executes an already-persisted job by ID. password is only used
// when the job is job_kind=database (never stored — must travel with the
// run_job command every time).
func (e *Executor) runJob(ctx context.Context, payload map[string]any) (map[string]any, error) {
	jobIDStr, _ := payload["job_id"].(string)
	password, _ := payload["password"].(string)
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
		var destination backup.Destination
		switch d.DestType {
		case "", "local":
			destination = backup.Local(d.Dest)
		case "nas":
			destination = backup.NAS(d.Dest)
		case "ssh":
			destination = backup.SSH(d.SSHHost, d.SSHPort, d.SSHUser, d.SSHKey, d.Dest)
		default:
			return nil, fmt.Errorf("unsupported destination_type: %s", d.DestType)
		}

		execID, err := e.Store.StartExecution(ctx, jobID, core.TriggerRemoteCentral)
		if err != nil {
			return nil, err
		}
		res, runErr := backup.Run(ctx, d.Source, destination, core.BackupType(d.BackupType), d.LinkDest)
		if runErr != nil {
			_ = e.Store.FinishExecution(ctx, execID, core.ExecFailed, 0, 0, runErr.Error())
			return nil, runErr
		}
		if err := e.Store.FinishExecution(ctx, execID, core.ExecSuccess, res.BytesTransferred, res.FilesTransferred, ""); err != nil {
			return nil, err
		}
		return map[string]any{
			"job_id": jobID.String(), "execution_id": execID.String(),
			"bytes_transferred": res.BytesTransferred, "files_transferred": res.FilesTransferred,
		}, nil

	case "database":
		d, err := e.Store.GetDBJobDetails(ctx, jobID)
		if err != nil {
			return nil, err
		}
		execID, err := e.Store.StartExecution(ctx, jobID, core.TriggerRemoteCentral)
		if err != nil {
			return nil, err
		}
		res, runErr := dbjob.Run(ctx, dbjob.Request{
			Engine: core.DBEngine(d.Engine), Host: d.Host, Port: d.Port, Username: d.Username, Password: password,
			Database: d.Database, SQLitePath: d.SQLitePath, Compression: core.Compression(d.Compression),
			DestType: d.DestType, DestPath: d.DestPath,
			SSHHost: d.SSHHost, SSHPort: d.SSHPort, SSHUser: d.SSHUser, SSHKey: d.SSHKey,
		})
		if runErr != nil {
			_ = e.Store.FinishExecution(ctx, execID, core.ExecFailed, 0, 0, runErr.Error())
			return nil, runErr
		}
		if err := e.Store.FinishExecution(ctx, execID, core.ExecSuccess, res.BytesWritten, 1, ""); err != nil {
			return nil, err
		}
		return map[string]any{
			"job_id": jobID.String(), "execution_id": execID.String(),
			"bytes_written": res.BytesWritten, "dest_path": res.DestPath,
		}, nil

	default:
		return nil, fmt.Errorf("unknown job kind: %s", kind)
	}
}
