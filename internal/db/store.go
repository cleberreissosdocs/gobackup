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

// CreateFileJob inserts (or reuses, by name) a job_kind=files backup job.
func (s *Store) CreateFileJob(ctx context.Context, tenantID, agentID uuid.UUID, name, sourcePath, destPath string, backupType core.BackupType, cron string) (uuid.UUID, error) {
	var jobID uuid.UUID
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO backup_jobs (tenant_id, agent_id, name, job_kind, source_path, backup_type, schedule_cron, enabled)
		VALUES ($1, $2, $3, 'files', $4, $5, $6, true)
		ON CONFLICT DO NOTHING
		RETURNING id
	`, tenantID, agentID, name, sourcePath, string(backupType), cron).Scan(&jobID)
	if err != nil {
		err = s.Pool.QueryRow(ctx, `SELECT id FROM backup_jobs WHERE agent_id=$1 AND name=$2`, agentID, name).Scan(&jobID)
		if err != nil {
			return uuid.Nil, fmt.Errorf("create file job: %w", err)
		}
	}
	// keep destination path current for re-runs with a different --dest
	_, _ = s.Pool.Exec(ctx, `UPDATE backup_jobs SET source_path=$1 WHERE id=$2`, sourcePath, jobID)
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
	return id, nil
}

// CreateDatabaseJob inserts (or reuses, by name) a job_kind=database backup job.
func (s *Store) CreateDatabaseJob(ctx context.Context, tenantID, agentID, dbConnectionID uuid.UUID, name string, compression core.Compression, cron string) (uuid.UUID, error) {
	var jobID uuid.UUID
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO backup_jobs (tenant_id, agent_id, name, job_kind, db_connection_id, compression, schedule_cron, enabled)
		VALUES ($1, $2, $3, 'database', $4, $5, $6, true)
		ON CONFLICT DO NOTHING
		RETURNING id
	`, tenantID, agentID, name, dbConnectionID, string(compression), cron).Scan(&jobID)
	if err != nil {
		err = s.Pool.QueryRow(ctx, `SELECT id FROM backup_jobs WHERE agent_id=$1 AND name=$2`, agentID, name).Scan(&jobID)
		if err != nil {
			return uuid.Nil, fmt.Errorf("create database job: %w", err)
		}
	}
	return jobID, nil
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
