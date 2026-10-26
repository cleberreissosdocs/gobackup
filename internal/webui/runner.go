// Package webui is the shared web admin UI mounted by both gobackup-agent
// (local, single-server administration) and gobackup-central (same screens,
// plus the multi-server dashboard). The two differ only in which Runner
// implementation executes jobs: LocalRunner runs rsync/dbdump directly on
// this host; CentralRunner enqueues a command for a remote agent and polls
// a cache refreshed on every heartbeat for status/progress.
package webui

import (
	"context"
	"time"
)

// DirEntry is one row returned by a directory listing.
type DirEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
}

// FileJobRequest describes a file-tree backup job to create or edit.
type FileJobRequest struct {
	Name       string
	Source     string
	Dest       string
	BackupType string // full|incremental|differential
	LinkDest   string
	DestType   string // local|nas|ssh
	SSHHost    string
	SSHPort    int
	SSHUser    string
	SSHKey     string
	Cron       string // 5-field cron expression, or "" for manual-only
	Enabled    bool
}

// DBJobRequest describes a database backup job to create or edit. The
// password is used only transiently to validate the form — it is never
// persisted, so running the job later (manually or on schedule) asks for
// it again (scheduled runs of password-protected engines will fail; see
// the UI hint).
type DBJobRequest struct {
	Name        string
	Engine      string // mariadb|mysql|postgresql|sqlite
	Host        string
	Port        int
	Username    string
	Password    string
	Database    string // or "__ALL__"
	SQLitePath  string
	Compression string // gzip|zip|none
	DestPath    string
	DestType    string
	SSHHost     string
	SSHPort     int
	SSHUser     string
	SSHKey      string
	Cron        string
	Enabled     bool
}

// JobOutcome is what the UI shows after creating/updating a job, or right
// after starting one (before progress polling takes over).
type JobOutcome struct {
	Status  string // "success" | "failed" | "pending" | "started"
	Message string
}

// JobSummary is one row in the "Tarefas" list — a saved job definition that
// can be run (again), edited, enabled/disabled, and optionally scheduled.
type JobSummary struct {
	ID      string
	Name    string
	Kind    string // files|database
	Cron    string
	Enabled bool
}

// ProgressInfo is what the "Executar" progress bar polls.
type ProgressInfo struct {
	Status  string // "idle" | "running" | "success" | "failed"
	Percent int    // 0-100, or -1 when indeterminate (e.g. database dumps)
	Message string
}

// ActivityItem is one row in the "recent activity" list on an agent's page.
type ActivityItem struct {
	When        time.Time
	Description string
	Status      string
}

// Runner is implemented once for local (agent) execution and once for
// remote (central, via the command queue + heartbeat-refreshed cache)
// execution.
type Runner interface {
	ListDir(ctx context.Context, path string) ([]DirEntry, error)

	// CreateFileJob/CreateDBJob persist a NEW job definition and return its
	// ID WITHOUT running it. Use RunJob to execute it (now or later).
	CreateFileJob(ctx context.Context, req FileJobRequest) (jobID string, err error)
	CreateDBJob(ctx context.Context, req DBJobRequest) (jobID string, err error)

	// GetFileJob/GetDBJob fetch an existing job's definition for the
	// "Editar" form (password is never returned/prefilled).
	GetFileJob(ctx context.Context, jobID string) (FileJobRequest, error)
	GetDBJob(ctx context.Context, jobID string) (DBJobRequest, error)

	// UpdateFileJob/UpdateDBJob overwrite an existing job's definition.
	UpdateFileJob(ctx context.Context, jobID string, req FileJobRequest) error
	UpdateDBJob(ctx context.Context, jobID string, req DBJobRequest) error

	ListJobs(ctx context.Context) ([]JobSummary, error)

	// DeleteJob removes a job definition entirely (and its execution
	// history). Does not touch files already written by past runs.
	DeleteJob(ctx context.Context, jobID string) error

	// RunJob STARTS an already-created job asynchronously and returns
	// immediately — poll JobStatus for live progress. password is only
	// used for job kind "database" when the engine requires authentication.
	RunJob(ctx context.Context, jobID, password string) (JobOutcome, error)

	// JobStatus reports the current/last-known progress for a job, for the
	// "Executar" progress bar to poll every ~1s.
	JobStatus(ctx context.Context, jobID string) (ProgressInfo, error)

	RecentActivity(ctx context.Context, limit int) ([]ActivityItem, error)
}
