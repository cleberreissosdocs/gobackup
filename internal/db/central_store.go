package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// EnsureDefaultTenant is the Central-side equivalent bootstrap: creates (or
// fetches) the single default tenant used while multi-tenant UI isn't built
// yet.
func (s *Store) EnsureDefaultTenant(ctx context.Context) (uuid.UUID, error) {
	var tenantID uuid.UUID
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO tenants (name)
		SELECT 'default'
		WHERE NOT EXISTS (SELECT 1 FROM tenants WHERE name = 'default')
		RETURNING id
	`).Scan(&tenantID)
	if err != nil {
		err = s.Pool.QueryRow(ctx, `SELECT id FROM tenants WHERE name = 'default'`).Scan(&tenantID)
		if err != nil {
			return uuid.Nil, fmt.Errorf("ensure default tenant: %w", err)
		}
	}
	return tenantID, nil
}

// RegisterAgent creates a new agent row under tenantID and returns its ID
// plus a freshly generated plaintext token. Only the SHA-256 hash of the
// token is stored; the plaintext is returned once and never persisted.
func (s *Store) RegisterAgent(ctx context.Context, tenantID uuid.UUID, name, hostname string) (agentID uuid.UUID, token string, err error) {
	token, err = generateToken()
	if err != nil {
		return uuid.Nil, "", err
	}
	tokenHash := hashToken(token)

	err = s.Pool.QueryRow(ctx, `
		INSERT INTO agents (tenant_id, name, hostname, api_token_hash, status)
		VALUES ($1, $2, $3, $4, 'unknown')
		RETURNING id
	`, tenantID, name, hostname, tokenHash).Scan(&agentID)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("register agent: %w", err)
	}
	return agentID, token, nil
}

// AuthenticateAgent resolves a bearer token to its agent ID.
func (s *Store) AuthenticateAgent(ctx context.Context, token string) (uuid.UUID, error) {
	tokenHash := hashToken(token)
	var agentID uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT id FROM agents WHERE api_token_hash = $1`, tokenHash).Scan(&agentID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("unknown token")
	}
	return agentID, nil
}

// TouchAgentHeartbeat records that an agent just polled in, successfully.
func (s *Store) TouchAgentHeartbeat(ctx context.Context, agentID uuid.UUID, status string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE agents SET status = 'online', last_seen_at = now(), notified_offline_at = NULL WHERE id = $1
	`, agentID)
	_ = status // reserved for future use (idle/running distinction in the dashboard)
	if err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	return nil
}

// QueuedCommand is a command delivered to the agent in a poll response.
type QueuedCommand struct {
	ID      uuid.UUID      `json:"id"`
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

// EnqueueCommand adds a command for the given agent to pick up on its next poll.
func (s *Store) EnqueueCommand(ctx context.Context, agentID uuid.UUID, cmdType string, payload map[string]any) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO agent_commands (agent_id, type, payload, status)
		VALUES ($1, $2, $3, 'pending')
		RETURNING id
	`, agentID, cmdType, payload).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("enqueue command: %w", err)
	}
	return id, nil
}

// FetchAndDeliverPendingCommands returns all pending commands for an agent
// and marks them as delivered in the same call.
func (s *Store) FetchAndDeliverPendingCommands(ctx context.Context, agentID uuid.UUID) ([]QueuedCommand, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, type, payload FROM agent_commands
		WHERE agent_id = $1 AND status = 'pending'
		ORDER BY created_at
	`, agentID)
	if err != nil {
		return nil, fmt.Errorf("fetch pending commands: %w", err)
	}
	defer rows.Close()

	var out []QueuedCommand
	var ids []uuid.UUID
	for rows.Next() {
		var c QueuedCommand
		if err := rows.Scan(&c.ID, &c.Type, &c.Payload); err != nil {
			return nil, fmt.Errorf("scan command: %w", err)
		}
		out = append(out, c)
		ids = append(ids, c.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(ids) > 0 {
		_, err := s.Pool.Exec(ctx, `
			UPDATE agent_commands SET status = 'delivered', delivered_at = now()
			WHERE id = ANY($1)
		`, ids)
		if err != nil {
			return nil, fmt.Errorf("mark delivered: %w", err)
		}
	}
	return out, nil
}

// RecordCommandResult stores the outcome an agent reported for a command it
// previously received.
func (s *Store) RecordCommandResult(ctx context.Context, commandID uuid.UUID, status string, result map[string]any) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE agent_commands SET status = $1, acked_at = now(), result = $2
		WHERE id = $3
	`, status, result, commandID)
	if err != nil {
		return fmt.Errorf("record command result: %w", err)
	}
	return nil
}

// GetCommand returns the current status/result of a queued command — used
// by the web UI to wait for a remote job's outcome after enqueueing it.
func (s *Store) GetCommand(ctx context.Context, id uuid.UUID) (status string, result map[string]any, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT status, result FROM agent_commands WHERE id = $1`, id).Scan(&status, &result)
	if err != nil {
		return "", nil, fmt.Errorf("get command: %w", err)
	}
	return status, result, nil
}

// CommandRow is a read model for an agent's recent command/activity log.
type CommandRow struct {
	Type      string
	Status    string
	CreatedAt time.Time
	Result    map[string]any
}

// ListRecentCommands returns the most recent commands sent to an agent,
// newest first — used as the Central's "recent activity" proxy for
// job_executions (which live on the agent's own local database).
func (s *Store) ListRecentCommands(ctx context.Context, agentID uuid.UUID, limit int) ([]CommandRow, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT type, status, created_at, result
		FROM agent_commands
		WHERE agent_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, agentID, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent commands: %w", err)
	}
	defer rows.Close()

	var out []CommandRow
	for rows.Next() {
		var c CommandRow
		if err := rows.Scan(&c.Type, &c.Status, &c.CreatedAt, &c.Result); err != nil {
			return nil, fmt.Errorf("scan command: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AgentTenantAndName resolves the tenant and display name for an agent, so
// callers (e.g. the notifier) can route an alert without a second round
// trip from the caller's side.
func (s *Store) AgentTenantAndName(ctx context.Context, agentID uuid.UUID) (tenantID uuid.UUID, name string, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT tenant_id, name FROM agents WHERE id = $1`, agentID).Scan(&tenantID, &name)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("agent tenant lookup: %w", err)
	}
	return tenantID, name, nil
}

// CreateNotificationChannel registers an email or telegram alert channel
// for a tenant. events is the list of event names that should trigger it
// (e.g. "job_failed", "agent_offline"); an empty list means "all events".
func (s *Store) CreateNotificationChannel(ctx context.Context, tenantID uuid.UUID, chType string, config map[string]any, events []string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO notification_channels (tenant_id, type, config, events)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, tenantID, chType, config, events).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("create notification channel: %w", err)
	}
	return id, nil
}

// NotificationChannelRow is a read model for dispatching alerts.
type NotificationChannelRow struct {
	ID     uuid.UUID
	Type   string
	Config map[string]any
	Events []string
}

// ListNotificationChannels returns every alert channel configured for a tenant.
func (s *Store) ListNotificationChannels(ctx context.Context, tenantID uuid.UUID) ([]NotificationChannelRow, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, type, config, events FROM notification_channels WHERE tenant_id = $1
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list notification channels: %w", err)
	}
	defer rows.Close()

	var out []NotificationChannelRow
	for rows.Next() {
		var c NotificationChannelRow
		var events []string
		if err := rows.Scan(&c.ID, &c.Type, &c.Config, &events); err != nil {
			return nil, fmt.Errorf("scan notification channel: %w", err)
		}
		c.Events = events
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkAgentOfflineNotified / ClearAgentOfflineNotified debounce the
// "agent went offline" alert so it fires once per outage, not on every
// dashboard poll/background check.
func (s *Store) MarkAgentOfflineNotified(ctx context.Context, agentID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `UPDATE agents SET notified_offline_at = now() WHERE id = $1`, agentID)
	return err
}

func (s *Store) ClearAgentOfflineNotified(ctx context.Context, agentID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `UPDATE agents SET notified_offline_at = NULL WHERE id = $1 AND notified_offline_at IS NOT NULL`, agentID)
	return err
}

// ListAgentsNeedingOfflineAlert returns agents that are stale (no heartbeat
// within staleAfter) and haven't already been alerted on for this outage.
func (s *Store) ListAgentsNeedingOfflineAlert(ctx context.Context) ([]AgentView, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, name, hostname, status, last_seen_at
		FROM agents
		WHERE notified_offline_at IS NULL
		  AND (last_seen_at IS NULL OR last_seen_at < now() - interval '5 seconds')
	`)
	if err != nil {
		return nil, fmt.Errorf("list agents needing offline alert: %w", err)
	}
	defer rows.Close()

	var out []AgentView
	for rows.Next() {
		var a AgentView
		if err := rows.Scan(&a.ID, &a.Name, &a.Hostname, &a.Status, &a.LastSeenAt); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AgentView is a read model for the Central dashboard.
type AgentView struct {
	ID         uuid.UUID
	Name       string
	Hostname   string
	Status     string
	LastSeenAt *time.Time
}

// staleAfter is how long since the last heartbeat before the dashboard
// should show an agent as offline, even if its DB row still says online.
// With a 1s heartbeat interval, 5s of silence (5 missed beats) is a solid
// signal without being trigger-happy on a single dropped packet.
const staleAfter = 5 * time.Second

func (s *Store) ListAgents(ctx context.Context) ([]AgentView, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, name, hostname, status, last_seen_at
		FROM agents
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	var out []AgentView
	for rows.Next() {
		var a AgentView
		if err := rows.Scan(&a.ID, &a.Name, &a.Hostname, &a.Status, &a.LastSeenAt); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		if a.LastSeenAt == nil || time.Since(*a.LastSeenAt) > staleAfter {
			a.Status = "offline"
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// RemoteJobInfo is one job entry as reported by an agent's heartbeat.
type RemoteJobInfo struct {
	JobID   string
	Name    string
	Kind    string
	Cron    string
	Enabled bool
}

// ReplaceAgentJobsSnapshot overwrites the cached job list for one agent.
// Called on every heartbeat (piggybacked on the poll, no extra round trip)
// so the Central's dashboard can show an up-to-date combined "Tarefas de
// todos os servidores" table without waiting on a live remote command.
func (s *Store) ReplaceAgentJobsSnapshot(ctx context.Context, agentID uuid.UUID, jobs []RemoteJobInfo) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("replace agent jobs snapshot: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM agent_jobs_cache WHERE agent_id=$1`, agentID); err != nil {
		return fmt.Errorf("replace agent jobs snapshot: clear: %w", err)
	}
	for _, j := range jobs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO agent_jobs_cache (agent_id, job_id, name, kind, cron, enabled) VALUES ($1, $2, $3, $4, $5, $6)
		`, agentID, j.JobID, j.Name, j.Kind, j.Cron, j.Enabled); err != nil {
			return fmt.Errorf("replace agent jobs snapshot: insert: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("replace agent jobs snapshot: commit: %w", err)
	}
	return nil
}

// AllAgentsJobRow is one row of the dashboard's combined jobs table.
type AllAgentsJobRow struct {
	AgentID   uuid.UUID
	AgentName string
	JobID     string
	Name      string
	Kind      string
	Cron      string
	Enabled   bool
}

// ListAllAgentJobs returns the cached job list across every agent, for the
// dashboard's "Tarefas de todos os servidores" table.
func (s *Store) ListAllAgentJobs(ctx context.Context) ([]AllAgentsJobRow, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT c.agent_id, a.name, c.job_id, c.name, c.kind, c.cron, c.enabled
		FROM agent_jobs_cache c
		JOIN agents a ON a.id = c.agent_id
		ORDER BY a.name, c.name
	`)
	if err != nil {
		return nil, fmt.Errorf("list all agent jobs: %w", err)
	}
	defer rows.Close()
	var out []AllAgentsJobRow
	for rows.Next() {
		var row AllAgentsJobRow
		if err := rows.Scan(&row.AgentID, &row.AgentName, &row.JobID, &row.Name, &row.Kind, &row.Cron, &row.Enabled); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// RemoteExecutionStatus is one running/just-finished execution reported by
// an agent's heartbeat (piggybacked, same mechanism as the jobs list).
type RemoteExecutionStatus struct {
	JobID   string
	Status  string
	Percent int
	Message string
}

// ReplaceAgentExecutionStatus overwrites the cached execution-status rows
// for one agent — called on every heartbeat.
func (s *Store) ReplaceAgentExecutionStatus(ctx context.Context, agentID uuid.UUID, rows []RemoteExecutionStatus) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("replace agent execution status: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM agent_execution_status WHERE agent_id=$1`, agentID); err != nil {
		return fmt.Errorf("replace agent execution status: clear: %w", err)
	}
	for _, row := range rows {
		if _, err := tx.Exec(ctx, `
			INSERT INTO agent_execution_status (agent_id, job_id, status, percent, message) VALUES ($1, $2, $3, $4, $5)
		`, agentID, row.JobID, row.Status, row.Percent, row.Message); err != nil {
			return fmt.Errorf("replace agent execution status: insert: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("replace agent execution status: commit: %w", err)
	}
	return nil
}

// GetAgentJobStatus looks up the cached execution status for one job on
// one agent, for the "Executar" progress bar to poll.
func (s *Store) GetAgentJobStatus(ctx context.Context, agentID uuid.UUID, jobID string) (status string, percent int, message string, found bool, err error) {
	err = s.Pool.QueryRow(ctx, `
		SELECT status, percent, message FROM agent_execution_status WHERE agent_id=$1 AND job_id=$2
	`, agentID, jobID).Scan(&status, &percent, &message)
	if err != nil {
		return "", 0, "", false, nil
	}
	return status, percent, message, true, nil
}

// DeleteAgent removes an agent and (via ON DELETE CASCADE on agents.id)
// every row that references it: destinations, db_connections, backup_jobs,
// job_executions, agent_commands, agent_jobs_cache, agent_execution_status.
// It does NOT touch anything on the agent's own machine — the agent keeps
// running and its local Postgres/jobs are untouched; it would just need to
// be registered again (via its own "Configurações" page) to reconnect.
func (s *Store) DeleteAgent(ctx context.Context, agentID uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM agents WHERE id=$1`, agentID)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("delete agent: not found")
	}
	return nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
