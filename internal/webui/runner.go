// Package webui is the shared web admin UI mounted by both gobackup-agent
// (local, single-server administration) and gobackup-central (same screens,
// plus the multi-server dashboard). The two differ only in which Runner
// implementation executes jobs: LocalRunner runs rsync/dbdump directly on
// this host; CentralRunner enqueues a command for a remote agent and polls
// a cache refreshed on every heartbeat for status/progress.
package webui

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// DirEntry is one row returned by a directory listing.
type DirEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
}

// FileJobRequest describes a file-tree backup job to create or edit.
type FileJobRequest struct {
	Name             string
	Source           string
	Dest             string
	BackupType       string // full|incremental|differential
	LinkDest         string
	DeleteExtraneous bool   // rsync --delete: mirror deletions from source to dest
	DestType         string // local|nas|ssh
	SSHHost          string
	SSHPort          int
	SSHUser          string
	SSHKey           string
	Cron             string // 5-field cron expression, or "" for manual-only
	Enabled          bool
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
	// RetentionDays: automatically delete this job's own past backup files
	// once older than this many days. 0 = keep forever (no auto-cleanup).
	RetentionDays int
	Cron          string
	Enabled       bool
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
	// Status is the most recent execution's status: "running" means a
	// backup for this job is active RIGHT NOW (across any trigger — manual,
	// scheduled, or a remote command) — the UI shows "Executando" as a link
	// to the live progress page. Otherwise "success"/"failed", or "" if the
	// job has never run.
	Status string
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

// triggerLabel maps a raw execution trigger to a short, human Portuguese
// phrase for the "Atividade recente" list.
func triggerLabel(trigger string) string {
	switch trigger {
	case "manual":
		return "manual"
	case "scheduled":
		return "agendamento"
	case "remote_central":
		return "Central"
	default:
		return trigger
	}
}

// shortenError trims a raw error (often multi-line and full of low-level
// tool output, e.g. rsync's stats dump) down to one short, scannable line.
func shortenError(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return ""
	}
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = strings.TrimSpace(msg[:i])
	}
	const maxLen = 140
	if len(msg) > maxLen {
		msg = strings.TrimSpace(msg[:maxLen]) + "…"
	}
	return msg
}

// humanizeErrorReason recognizes the most common low-level failure
// patterns (rsync/dump/ssh output) and returns a short plain-Portuguese
// explanation instead of the raw technical message. Falls back to a
// shortened version of the original message when nothing matches, so
// nothing is ever silently hidden — just made more readable when possible.
func humanizeErrorReason(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "no such file or directory"):
		return "caminho de origem ou destino não encontrado"
	case strings.Contains(lower, "permission denied"):
		return "permissão negada ao acessar o caminho"
	case strings.Contains(lower, "connection refused"):
		return "não foi possível conectar ao destino (conexão recusada)"
	case strings.Contains(lower, "connection timed out"), strings.Contains(lower, "operation timed out"):
		return "não foi possível conectar ao destino (tempo esgotado)"
	case strings.Contains(lower, "no route to host"):
		return "não foi possível conectar ao destino (host inacessível)"
	case strings.Contains(lower, "authentication failed"), strings.Contains(lower, "password authentication failed"):
		return "usuário ou senha incorretos"
	case strings.Contains(lower, "could not translate host name"), strings.Contains(lower, "name or service not known"):
		return "endereço do servidor não encontrado"
	case strings.Contains(lower, "no space left on device"):
		return "sem espaço em disco no destino"
	case strings.Contains(lower, "host key verification failed"):
		return "chave SSH do host de destino não reconhecida"
	default:
		return shortenError(msg)
	}
}

// humanizeExecution turns one job execution into a plain sentence instead
// of a raw "jobname (trigger): <huge technical dump>" string.
func humanizeExecution(jobName, status, trigger, errMsg string) string {
	trig := triggerLabel(trigger)
	switch status {
	case "success":
		return fmt.Sprintf("Backup \"%s\" concluído com sucesso (%s)", jobName, trig)
	case "running":
		return fmt.Sprintf("Backup \"%s\" em execução (%s)", jobName, trig)
	case "cancelled":
		return fmt.Sprintf("Backup \"%s\" cancelado (%s)", jobName, trig)
	case "failed":
		if reason := humanizeErrorReason(errMsg); reason != "" {
			return fmt.Sprintf("Backup \"%s\" falhou (%s): %s", jobName, trig, reason)
		}
		return fmt.Sprintf("Backup \"%s\" falhou (%s)", jobName, trig)
	default:
		return fmt.Sprintf("Backup \"%s\" (%s)", jobName, trig)
	}
}

// commandLabel maps a raw Central->Agent command type to a short Portuguese
// phrase, for the "Atividade recente" list shown when administering an
// agent remotely (this log is the command/protocol history, since the
// actual job_executions table lives on the agent's own database).
func commandLabel(cmdType string) string {
	switch cmdType {
	case "create_job":
		return "Tarefa criada"
	case "update_job":
		return "Tarefa atualizada"
	case "delete_job":
		return "Tarefa apagada"
	case "run_job":
		return "Execução solicitada"
	case "list_jobs":
		return "Lista de tarefas atualizada"
	case "get_job":
		return "Dados da tarefa consultados"
	case "list_dir":
		return "Diretório consultado"
	default:
		return cmdType
	}
}
