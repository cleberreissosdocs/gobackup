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
		  AND (last_seen_at IS NULL OR last_seen_at < now() - interval '45 seconds')
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
// should show an agent as offline, even if its DB row still says online
// (polling interval is 15s, so >45s missed = 3 missed beats).
const staleAfter = 45 * time.Second

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

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
