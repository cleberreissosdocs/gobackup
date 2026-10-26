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

// FileJobRequest describes a file-tree backup to create and run immediately.
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

// DBJobRequest describes a database backup to create and run immediately.
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

// JobOutcome is what the UI shows after submitting a job.
type JobOutcome struct {
	Status  string // "success" | "failed" | "pending"
	Message string
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
	RunFileJob(ctx context.Context, req FileJobRequest) (JobOutcome, error)
	RunDBJob(ctx context.Context, req DBJobRequest) (JobOutcome, error)
	RecentActivity(ctx context.Context, limit int) ([]ActivityItem, error)
}
