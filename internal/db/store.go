// Package db provides a thin pgx-based data access layer shared by the
// agent and central binaries.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cleber/gobackup/internal/core"
)

type Store struct {
	Pool *pgxpool.Pool
}

func Connect(ctx context.Context, connString string) (*Store, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("db connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("db ping: %w", err)
	}
	return &Store{Pool: pool}, nil
}

func (s *Store) Close() {
	s.Pool.Close()
}

// EnsureDefaultTenantAndAgent is a bootstrap helper for the single-tenant
// MVP: it creates a default tenant and registers this host as an agent if
// they don't already exist, returning their IDs.
func (s *Store) EnsureDefaultTenantAndAgent(ctx context.Context, hostname string) (tenantID, agentID uuid.UUID, err error) {
	err = s.Pool.QueryRow(ctx, `
		INSERT INTO tenants (name)
		SELECT 'default'
		WHERE NOT EXISTS (SELECT 1 FROM tenants WHERE name = 'default')
		RETURNING id
	`).Scan(&tenantID)
	if err != nil {
		// already exists: fetch it
		err = s.Pool.QueryRow(ctx, `SELECT id FROM tenants WHERE name = 'default'`).Scan(&tenantID)
		if err != nil {
			return uuid.Nil, uuid.Nil, fmt.Errorf("ensure tenant: %w", err)
		}
	}

	err = s.Pool.QueryRow(ctx, `
		INSERT INTO agents (tenant_id, name, hostname, api_token_hash, status)
		SELECT $1, $2, $2, 'local', 'online'
		WHERE NOT EXISTS (SELECT 1 FROM agents WHERE hostname = $2)
		RETURNING id
	`, tenantID, hostname).Scan(&agentID)
	if err != nil {
		err = s.Pool.QueryRow(ctx, `SELECT id FROM agents WHERE hostname = $1`, hostname).Scan(&agentID)
		if err != nil {
			return uuid.Nil, uuid.Nil, fmt.Errorf("ensure agent: %w", err)
		}
	}
	return tenantID, agentID, nil
}

// CreateUser inserts a new login (used by the `create-user` CLI bootstrap).
// The provided tenantID may be uuid.Nil for a single-tenant MVP deployment.
func (s *Store) CreateUser(ctx context.Context, tenantID uuid.UUID, email, passwordHash string, role core.UserRole) (uuid.UUID, error) {
	var id uuid.UUID
	var tid any
	if tenantID != uuid.Nil {
		tid = tenantID
	}
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO users (tenant_id, email, password_hash, role)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (email) DO UPDATE SET password_hash = EXCLUDED.password_hash
		RETURNING id
	`, tid, email, passwordHash, string(role)).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("create user: %w", err)
	}
	return id, nil
}

// GetUserPasswordHash looks up a user by email for login verification.
func (s *Store) GetUserPasswordHash(ctx context.Context, email string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var hash string
	err := s.Pool.QueryRow(ctx, `SELECT id, password_hash FROM users WHERE email = $1`, email).Scan(&id, &hash)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("user not found: %w", err)
	}
	return id, hash, nil
}

// CreateFileJob inserts (or reuses, by name) a job_kind=files backup job,
// persisting the full destination definition (not just source_path) so the
// job can be re-run later from "Tarefas" without resubmitting the form.
func (s *Store) CreateFileJob(ctx context.Context, tenantID, agentID uuid.UUID, name, sourcePath, destPath string, backupType core.BackupType, destType, linkDest, sshHost string, sshPort int, sshUser, sshKeyPath, cron string) (uuid.UUID, error) {
	var jobID uuid.UUID
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO backup_jobs (tenant_id, agent_id, name, job_kind, source_path, backup_type, dest_path, dest_type, link_dest, ssh_host, ssh_port, ssh_user, ssh_key_path, schedule_cron, enabled)
		VALUES ($1, $2, $3, 'files', $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, true)
		ON CONFLICT DO NOTHING
		RETURNING id
	`, tenantID, agentID, name, sourcePath, string(backupType), destPath, destType, linkDest, sshHost, sshPort, sshUser, sshKeyPath, cron).Scan(&jobID)
	if err != nil {
		err = s.Pool.QueryRow(ctx, `SELECT id FROM backup_jobs WHERE agent_id=$1 AND name=$2`, agentID, name).Scan(&jobID)
		if err != nil {
			return uuid.Nil, fmt.Errorf("create file job: %w", err)
		}
	}
	// keep the full definition current if the same name is reused with new settings
	_, _ = s.Pool.Exec(ctx, `
		UPDATE backup_jobs
		SET source_path=$1, backup_type=$2, dest_path=$3, dest_type=$4, link_dest=$5,
		    ssh_host=$6, ssh_port=$7, ssh_user=$8, ssh_key_path=$9, updated_at=now()
		WHERE id=$10
	`, sourcePath, string(backupType), destPath, destType, linkDest, sshHost, sshPort, sshUser, sshKeyPath, jobID)
	return jobID, nil
}

// CreateDBConnection inserts (or reuses, by name) a db_connections row for
// job_kind=database backups.
func (s *Store) CreateDBConnection(ctx context.Context, agentID uuid.UUID, name string, engine core.DBEngine, host string, port int, username, passwordRef, databaseName, sqlitePath string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO db_connections (agent_id, name, engine, host, port, username, password_ref, database_name, sqlite_path)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT DO NOTHING
		RETURNING id
	`, agentID, name, string(engine), host, port, username, passwordRef, databaseName, sqlitePath).Scan(&id)
	if err != nil {
		err = s.Pool.QueryRow(ctx, `SELECT id FROM db_connections WHERE agent_id=$1 AND name=$2`, agentID, name).Scan(&id)
		if err != nil {
			return uuid.Nil, fmt.Errorf("create db connection: %w", err)
		}
	}
	// keep the connection details current if the same name is reused
	_, _ = s.Pool.Exec(ctx, `
		UPDATE db_connections
		SET engine=$1, host=$2, port=$3, username=$4, database_name=$5, sqlite_path=$6
		WHERE id=$7
	`, string(engine), host, port, username, databaseName, sqlitePath, id)
	return id, nil
}

// CreateDatabaseJob inserts (or reuses, by name) a job_kind=database backup
// job, persisting the dump destination too (dest_path/type/ssh_*) so it can
// be re-run later without resubmitting the form.
func (s *Store) CreateDatabaseJob(ctx context.Context, tenantID, agentID, dbConnectionID uuid.UUID, name string, compression core.Compression, destPath, destType, sshHost string, sshPort int, sshUser, sshKeyPath, cron string) (uuid.UUID, error) {
	var jobID uuid.UUID
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO backup_jobs (tenant_id, agent_id, name, job_kind, db_connection_id, compression, dest_path, dest_type, ssh_host, ssh_port, ssh_user, ssh_key_path, schedule_cron, enabled)
		VALUES ($1, $2, $3, 'database', $4, $5, $6, $7, $8, $9, $10, $11, $12, true)
		ON CONFLICT DO NOTHING
		RETURNING id
	`, tenantID, agentID, name, dbConnectionID, string(compression), destPath, destType, sshHost, sshPort, sshUser, sshKeyPath, cron).Scan(&jobID)
	if err != nil {
		err = s.Pool.QueryRow(ctx, `SELECT id FROM backup_jobs WHERE agent_id=$1 AND name=$2`, agentID, name).Scan(&jobID)
		if err != nil {
			return uuid.Nil, fmt.Errorf("create database job: %w", err)
		}
	}
	_, _ = s.Pool.Exec(ctx, `
		UPDATE backup_jobs
		SET db_connection_id=$1, compression=$2, dest_path=$3, dest_type=$4,
		    ssh_host=$5, ssh_port=$6, ssh_user=$7, ssh_key_path=$8, updated_at=now()
		WHERE id=$9
	`, dbConnectionID, string(compression), destPath, destType, sshHost, sshPort, sshUser, sshKeyPath, jobID)
	return jobID, nil
}

// JobSummary is a lightweight row for the "Tarefas" list.
type JobSummary struct {
	ID         uuid.UUID
	Name       string
	Kind       string // files|database
	Cron       string
	Enabled    bool
	LastStatus string // success|failed|running|"" (never run)
	CreatedAt  time.Time
}

// ListJobs returns every backup job defined for an agent, newest first,
// including each job's most recent execution status (LastStatus) — used
// both for the UI and piggybacked onto the heartbeat so the Central can
// show a per-agent health indicator without a live round trip.
func (s *Store) ListJobs(ctx context.Context, agentID uuid.UUID) ([]JobSummary, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT bj.id, bj.name, bj.job_kind, bj.schedule_cron, bj.enabled, bj.created_at,
		       COALESCE(je.status, '') AS last_status
		FROM backup_jobs bj
		LEFT JOIN LATERAL (
			SELECT status FROM job_executions WHERE job_id = bj.id ORDER BY started_at DESC NULLS LAST LIMIT 1
		) je ON true
		WHERE bj.agent_id=$1
		ORDER BY bj.created_at DESC
	`, agentID)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	var out []JobSummary
	for rows.Next() {
		var j JobSummary
		if err := rows.Scan(&j.ID, &j.Name, &j.Kind, &j.Cron, &j.Enabled, &j.CreatedAt, &j.LastStatus); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

// DeleteJob removes a job definition. Cascades (via FK) to its execution
// history. It does NOT touch files already written to the destination by
// past runs — only the job's definition and its run log are removed.
func (s *Store) DeleteJob(ctx context.Context, jobID uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM backup_jobs WHERE id=$1`, jobID)
	if err != nil {
		return fmt.Errorf("delete job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("delete job: not found")
	}
	return nil
}

// SchedulableJob is one row the background scheduler evaluates every tick.
type SchedulableJob struct {
	ID   uuid.UUID
	Kind string
	Cron string
}

// ListSchedulableJobs returns every enabled job with a non-empty cron
// expression for an agent, for the scheduler loop to evaluate.
func (s *Store) ListSchedulableJobs(ctx context.Context, agentID uuid.UUID) ([]SchedulableJob, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, job_kind, schedule_cron FROM backup_jobs
		WHERE agent_id=$1 AND enabled=true AND schedule_cron IS NOT NULL AND schedule_cron <> ''
	`, agentID)
	if err != nil {
		return nil, fmt.Errorf("list schedulable jobs: %w", err)
	}
	defer rows.Close()
	var out []SchedulableJob
	for rows.Next() {
		var j SchedulableJob
		if err := rows.Scan(&j.ID, &j.Kind, &j.Cron); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// GetJobKind returns whether a job is "files" or "database", so the caller
// knows which of GetFileJobDetails/GetDBJobDetails to use.
func (s *Store) GetJobKind(ctx context.Context, jobID uuid.UUID) (string, error) {
	var kind string
	err := s.Pool.QueryRow(ctx, `SELECT job_kind FROM backup_jobs WHERE id=$1`, jobID).Scan(&kind)
	if err != nil {
		return "", fmt.Errorf("get job kind: %w", err)
	}
	return kind, nil
}

// FileJobDetails is everything needed to re-run or edit a persisted file job.
type FileJobDetails struct {
	Name       string
	Source     string
	Dest       string
	BackupType string
	LinkDest   string
	DestType   string
	SSHHost    string
	SSHPort    int
	SSHUser    string
	SSHKey     string
	Cron       string
	Enabled    bool
}

func (s *Store) GetFileJobDetails(ctx context.Context, jobID uuid.UUID) (FileJobDetails, error) {
	var d FileJobDetails
	var linkDest, destType, sshHost, sshUser, sshKey *string
	var sshPort *int
	err := s.Pool.QueryRow(ctx, `
		SELECT name, source_path, dest_path, backup_type, link_dest, dest_type, ssh_host, ssh_port, ssh_user, ssh_key_path, schedule_cron, enabled
		FROM backup_jobs WHERE id=$1 AND job_kind='files'
	`, jobID).Scan(&d.Name, &d.Source, &d.Dest, &d.BackupType, &linkDest, &destType, &sshHost, &sshPort, &sshUser, &sshKey, &d.Cron, &d.Enabled)
	if err != nil {
		return d, fmt.Errorf("get file job details: %w", err)
	}
	if linkDest != nil {
		d.LinkDest = *linkDest
	}
	if destType != nil {
		d.DestType = *destType
	}
	if sshHost != nil {
		d.SSHHost = *sshHost
	}
	if sshPort != nil {
		d.SSHPort = *sshPort
	}
	if sshUser != nil {
		d.SSHUser = *sshUser
	}
	if sshKey != nil {
		d.SSHKey = *sshKey
	}
	return d, nil
}

// DBJobDetails is everything needed to re-run or edit a persisted database
// job, EXCEPT the password — that is never persisted, and must be supplied
// again at run time (unless the engine is sqlite/trust-auth, which needs
// none).
type DBJobDetails struct {
	Name        string
	Engine      string
	Host        string
	Port        int
	Username    string
	Database    string
	SQLitePath  string
	Compression string
	DestPath    string
	DestType    string
	SSHHost     string
	SSHPort     int
	SSHUser     string
	SSHKey      string
	Cron        string
	Enabled     bool
}

func (s *Store) GetDBJobDetails(ctx context.Context, jobID uuid.UUID) (DBJobDetails, error) {
	var d DBJobDetails
	var destType, sshHost, sshUser, sshKey *string
	var sshPort *int
	err := s.Pool.QueryRow(ctx, `
		SELECT bj.name, dc.engine, dc.host, dc.port, dc.username, dc.database_name, dc.sqlite_path,
		       bj.compression, bj.dest_path, bj.dest_type, bj.ssh_host, bj.ssh_port, bj.ssh_user, bj.ssh_key_path,
		       bj.schedule_cron, bj.enabled
		FROM backup_jobs bj
		JOIN db_connections dc ON dc.id = bj.db_connection_id
		WHERE bj.id=$1 AND bj.job_kind='database'
	`, jobID).Scan(&d.Name, &d.Engine, &d.Host, &d.Port, &d.Username, &d.Database, &d.SQLitePath,
		&d.Compression, &d.DestPath, &destType, &sshHost, &sshPort, &sshUser, &sshKey, &d.Cron, &d.Enabled)
	if err != nil {
		return d, fmt.Errorf("get db job details: %w", err)
	}
	if destType != nil {
		d.DestType = *destType
	}
	if sshHost != nil {
		d.SSHHost = *sshHost
	}
	if sshPort != nil {
		d.SSHPort = *sshPort
	}
	if sshUser != nil {
		d.SSHUser = *sshUser
	}
	if sshKey != nil {
		d.SSHKey = *sshKey
	}
	return d, nil
}

// StartExecution records a new running execution for a job.
func (s *Store) StartExecution(ctx context.Context, jobID uuid.UUID, trigger core.ExecutionTrigger) (uuid.UUID, error) {
	var execID uuid.UUID
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO job_executions (job_id, status, trigger, started_at)
		VALUES ($1, 'running', $2, now())
		RETURNING id
	`, jobID, string(trigger)).Scan(&execID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("start execution: %w", err)
	}
	return execID, nil
}

// FinishExecution marks an execution as success or failed with results.
func (s *Store) FinishExecution(ctx context.Context, execID uuid.UUID, status core.ExecutionStatus, bytesTransferred, filesTransferred int64, errMsg string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE job_executions
		SET status=$1, finished_at=now(), bytes_transferred=$2, files_transferred=$3, error_message=$4
		WHERE id=$5
	`, string(status), bytesTransferred, filesTransferred, errMsg, execID)
	if err != nil {
		return fmt.Errorf("finish execution: %w", err)
	}
	return nil
}

// LatestExecution is the most recent execution of a job — used by the
// progress-polling endpoint to show a final result after the in-memory
// progress registry entry has expired (e.g. after a page reload).
type LatestExecution struct {
	Status       string
	ErrorMessage string
	Found        bool
}

func (s *Store) GetLatestExecution(ctx context.Context, jobID uuid.UUID) (LatestExecution, error) {
	var le LatestExecution
	err := s.Pool.QueryRow(ctx, `
		SELECT status, COALESCE(error_message, '')
		FROM job_executions WHERE job_id=$1 ORDER BY started_at DESC NULLS LAST LIMIT 1
	`, jobID).Scan(&le.Status, &le.ErrorMessage)
	if err != nil {
		return LatestExecution{}, nil // no executions yet — not an error
	}
	le.Found = true
	return le, nil
}

// UpdateFileJobByID overwrites an existing file job's definition in place
// (used by the "Editar" form — unlike CreateFileJob, this never inserts).
func (s *Store) UpdateFileJobByID(ctx context.Context, jobID uuid.UUID, name, sourcePath, destPath string, backupType core.BackupType, destType, linkDest, sshHost string, sshPort int, sshUser, sshKeyPath, cron string, enabled bool) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE backup_jobs
		SET name=$1, source_path=$2, dest_path=$3, backup_type=$4, dest_type=$5, link_dest=$6,
		    ssh_host=$7, ssh_port=$8, ssh_user=$9, ssh_key_path=$10, schedule_cron=$11, enabled=$12, updated_at=now()
		WHERE id=$13 AND job_kind='files'
	`, name, sourcePath, destPath, string(backupType), destType, linkDest, sshHost, sshPort, sshUser, sshKeyPath, cron, enabled, jobID)
	if err != nil {
		return fmt.Errorf("update file job: %w", err)
	}
	return nil
}

// UpdateDatabaseJobByID overwrites an existing database job's definition
// (and its underlying db_connections row) in place.
func (s *Store) UpdateDatabaseJobByID(ctx context.Context, jobID uuid.UUID, name string, engine core.DBEngine, host string, port int, username, databaseName, sqlitePath string, compression core.Compression, destPath, destType, sshHost string, sshPort int, sshUser, sshKeyPath, cron string, enabled bool) error {
	var connID uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT db_connection_id FROM backup_jobs WHERE id=$1 AND job_kind='database'`, jobID).Scan(&connID)
	if err != nil {
		return fmt.Errorf("update database job: lookup connection: %w", err)
	}
	if _, err := s.Pool.Exec(ctx, `
		UPDATE db_connections SET name=$1, engine=$2, host=$3, port=$4, username=$5, database_name=$6, sqlite_path=$7
		WHERE id=$8
	`, name+"-connection", string(engine), host, port, username, databaseName, sqlitePath, connID); err != nil {
		return fmt.Errorf("update database job: connection: %w", err)
	}
	if _, err := s.Pool.Exec(ctx, `
		UPDATE backup_jobs
		SET name=$1, compression=$2, dest_path=$3, dest_type=$4, ssh_host=$5, ssh_port=$6, ssh_user=$7, ssh_key_path=$8,
		    schedule_cron=$9, enabled=$10, updated_at=now()
		WHERE id=$11 AND job_kind='database'
	`, name, string(compression), destPath, destType, sshHost, sshPort, sshUser, sshKeyPath, cron, enabled, jobID); err != nil {
		return fmt.Errorf("update database job: %w", err)
	}
	return nil
}

// SetSetting upserts a single key/value pair in agent_settings — used to
// store the Central URL/CA/token so the agent can be pointed at a Central
// from the web UI instead of only via CLI flags.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO agent_settings (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, key, value)
	if err != nil {
		return fmt.Errorf("set setting %s: %w", key, err)
	}
	return nil
}

// GetSetting returns a setting's value and whether it was found.
func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.Pool.QueryRow(ctx, `SELECT value FROM agent_settings WHERE key = $1`, key).Scan(&value)
	if err != nil {
		return "", false, nil //nolint:nilerr // "not found" is a normal, expected case here
	}
	return value, true, nil
}

// RecentExecution is a read model combining a job_executions row with its
// parent job's name, for the agent's "recent activity" page.
type RecentExecution struct {
	JobName      string
	Status       string
	Trigger      string
	StartedAt    time.Time
	ErrorMessage string
}

// ListRecentExecutions returns the most recent executions across all jobs
// for an agent, newest first.
func (s *Store) ListRecentExecutions(ctx context.Context, agentID uuid.UUID, limit int) ([]RecentExecution, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT bj.name, je.status, je.trigger, COALESCE(je.started_at, now()), COALESCE(je.error_message, '')
		FROM job_executions je
		JOIN backup_jobs bj ON bj.id = je.job_id
		WHERE bj.agent_id = $1
		ORDER BY je.started_at DESC NULLS LAST
		LIMIT $2
	`, agentID, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent executions: %w", err)
	}
	defer rows.Close()

	var out []RecentExecution
	for rows.Next() {
		var re RecentExecution
		if err := rows.Scan(&re.JobName, &re.Status, &re.Trigger, &re.StartedAt, &re.ErrorMessage); err != nil {
			return nil, fmt.Errorf("scan recent execution: %w", err)
		}
		out = append(out, re)
	}
	return out, rows.Err()
}
