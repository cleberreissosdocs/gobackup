// Package core contains the shared domain entities used by both the
// gobackup-agent and gobackup-central binaries.
package core

import (
	"time"

	"github.com/google/uuid"
)

// UserRole is the access level of a human user within a tenant.
type UserRole string

const (
	RoleAdmin    UserRole = "admin"
	RoleOperator UserRole = "operator"
	RoleViewer   UserRole = "viewer"
)

// AgentStatus reflects the last known liveness of an agent, derived from
// the polling heartbeat (see internal/poller and internal/queue).
type AgentStatus string

const (
	AgentOnline  AgentStatus = "online"
	AgentOffline AgentStatus = "offline"
	AgentUnknown AgentStatus = "unknown"
)

// DestinationType is where a backup (file copy or database dump) is written.
type DestinationType string

const (
	DestinationLocal DestinationType = "local"
	DestinationNAS   DestinationType = "nas"
	DestinationSSH   DestinationType = "ssh"
)

// DBEngine is a supported database engine for job_kind=database jobs.
type DBEngine string

const (
	EngineMariaDB    DBEngine = "mariadb"
	EngineMySQL      DBEngine = "mysql"
	EnginePostgreSQL DBEngine = "postgresql"
	EngineSQLite     DBEngine = "sqlite"
)

// AllDatabasesSentinel is the database_name value meaning "dump every
// database/schema on the server" instead of a single named database.
const AllDatabasesSentinel = "__ALL__"

// JobKind distinguishes a file-tree backup job from a database dump job.
type JobKind string

const (
	JobKindFiles    JobKind = "files"
	JobKindDatabase JobKind = "database"
)

// BackupType applies to JobKindFiles jobs.
type BackupType string

const (
	BackupFull         BackupType = "full"
	BackupIncremental  BackupType = "incremental"
	BackupDifferential BackupType = "differential"
)

// Compression applies to JobKindDatabase jobs.
type Compression string

const (
	CompressionGzip Compression = "gzip"
	CompressionZip  Compression = "zip"
	CompressionNone Compression = "none"
)

// ExecutionStatus is the lifecycle state of a single job execution.
type ExecutionStatus string

const (
	ExecQueued    ExecutionStatus = "queued"
	ExecRunning   ExecutionStatus = "running"
	ExecSuccess   ExecutionStatus = "success"
	ExecFailed    ExecutionStatus = "failed"
	ExecCancelled ExecutionStatus = "cancelled"
)

// ExecutionTrigger records what caused a job execution to run.
type ExecutionTrigger string

const (
	TriggerScheduled     ExecutionTrigger = "scheduled"
	TriggerManual        ExecutionTrigger = "manual"
	TriggerRemoteCentral ExecutionTrigger = "remote_central"
)

// CommandType is a remote instruction the Central can queue for an Agent.
type CommandType string

const (
	CommandCreateJob CommandType = "create_job"
	CommandUpdateJob CommandType = "update_job"
	CommandDeleteJob CommandType = "delete_job"
	CommandStartJob  CommandType = "start_job"
	CommandCancelJob CommandType = "cancel_job"
	CommandListDir   CommandType = "list_dir"
)

// CommandStatus is the delivery/ack lifecycle of a queued agent command.
type CommandStatus string

const (
	CommandPending   CommandStatus = "pending"
	CommandDelivered CommandStatus = "delivered"
	CommandAcked     CommandStatus = "acked"
	CommandFailed    CommandStatus = "failed"
)

// NotificationType is a channel the Central can use to alert on failures.
type NotificationType string

const (
	NotifyEmail    NotificationType = "email"
	NotifyTelegram NotificationType = "telegram"
)

type Tenant struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
}

type User struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	Email        string
	PasswordHash string
	Role         UserRole
	CreatedAt    time.Time
}

type Agent struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	Name          string
	Hostname      string
	APITokenHash  string
	Status        AgentStatus
	LastSeenAt    *time.Time
	AgentVersion  string
	CreatedAt     time.Time
}

type Destination struct {
	ID        uuid.UUID
	AgentID   uuid.UUID
	Type      DestinationType
	Path      string
	NASConfig map[string]any
	SSHConfig map[string]any
	CreatedAt time.Time
}

type DBConnection struct {
	ID           uuid.UUID
	AgentID      uuid.UUID
	Name         string
	Engine       DBEngine
	Host         string
	Port         int
	Username     string
	PasswordRef  string
	DatabaseName string // AllDatabasesSentinel or a specific database/schema name
	SQLitePath   string
	CreatedAt    time.Time
}

// IsAllDatabases reports whether this connection is configured to dump
// every database on the server rather than a single named one.
func (c DBConnection) IsAllDatabases() bool {
	return c.DatabaseName == AllDatabasesSentinel
}

type RetentionPolicy struct {
	KeepDaily   int `json:"keep_daily,omitempty"`
	KeepWeekly  int `json:"keep_weekly,omitempty"`
	KeepMonthly int `json:"keep_monthly,omitempty"`
}

type BackupJob struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	AgentID  uuid.UUID
	Name     string
	Kind     JobKind

	// JobKindFiles
	SourcePath    string
	DestinationID *uuid.UUID
	BackupType    BackupType

	// JobKindDatabase
	DBConnectionID     *uuid.UUID
	DumpDestinationID  *uuid.UUID
	Compression        Compression

	ScheduleCron     string
	RetentionPolicy  RetentionPolicy
	Enabled          bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type JobExecution struct {
	ID                uuid.UUID
	JobID             uuid.UUID
	Status            ExecutionStatus
	Trigger           ExecutionTrigger
	StartedAt         *time.Time
	FinishedAt        *time.Time
	BytesTransferred  int64
	FilesTransferred  int64
	ErrorMessage      string
	LogRef            string
}

type AgentCommand struct {
	ID          uuid.UUID
	AgentID     uuid.UUID
	Type        CommandType
	Payload     map[string]any
	Status      CommandStatus
	CreatedAt   time.Time
	DeliveredAt *time.Time
	AckedAt     *time.Time
	Result      map[string]any
}

type NotificationChannel struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Type      NotificationType
	Config    map[string]any
	Events    []string
	CreatedAt time.Time
}
