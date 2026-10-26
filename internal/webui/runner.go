// Package webui is the shared web admin UI mounted by both gobackup-agent
// (local, single-server administration) and gobackup-central (same screens,
// plus the multi-server dashboard). The two differ only in which Runner
// implementation executes jobs: LocalRunner runs rsync/dbdump directly on
// this host; CentralRunner enqueues a command for a remote agent and waits
// briefly for the result over the normal polling protocol.
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

// FileJobRequest describes a file-tree backup job to CREATE (not run).
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
}

// DBJobRequest describes a database backup job to CREATE (not run). The
// password is used only transiently to validate the form — it is never
// persisted, so running the job later asks for it again.
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
}

// JobOutcome is what the UI shows after creating or running a job.
type JobOutcome struct {
	Status  string // "success" | "failed" | "pending"
	Message string
}

// JobSummary is one row in the "Tarefas" list — a saved job definition that
// can be run (again) with the "Executar" button.
type JobSummary struct {
	ID   string
	Name string
	Kind string // files|database
}

// ActivityItem is one row in the "recent activity" list on an agent's page.
type ActivityItem struct {
	When        time.Time
	Description string
	Status      string
}

// Runner is implemented once for local (agent) execution and once for
// remote (central, via the command queue) execution.
type Runner interface {
	ListDir(ctx context.Context, path string) ([]DirEntry, error)

	// CreateFileJob/CreateDBJob persist a job definition and return its ID
	// WITHOUT running it. Use RunJob to actually execute it (now or later).
	CreateFileJob(ctx context.Context, req FileJobRequest) (jobID string, err error)
	CreateDBJob(ctx context.Context, req DBJobRequest) (jobID string, err error)

	ListJobs(ctx context.Context) ([]JobSummary, error)

	// RunJob executes an already-created job by ID. password is only used
	// for job kind "database" when the engine requires authentication
	// (ignored otherwise); it is never read from storage.
	RunJob(ctx context.Context, jobID, password string) (JobOutcome, error)

	RecentActivity(ctx context.Context, limit int) ([]ActivityItem, error)
}
