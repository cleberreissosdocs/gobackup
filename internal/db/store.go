// Package db provides a thin pgx-based data access layer shared by the
// agent and central binaries.
package db

import (
	"context"
	"fmt"

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
