// Package main (store.go): local job persistence for gobackup-winagent.
// There is no Postgres on this platform, so job definitions live in a
// small JSON file instead of a database — perfectly fine at the scale of
// "jobs for one server" this agent manages. A single Job struct covers
// both kinds (files/database) with a Kind discriminator, the same way the
// Linux agent's single `jobs` table does — this keeps get/update/delete/
// run_job (which only carry a job_id, no kind) unambiguous: look the job
// up once, branch on .Kind.
package winagentcore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Job mirrors the fields Central sends/expects for both job_kind="files"
// and job_kind="database" (see internal/webui/central_runner.go's
// fileJobPayload/dbJobPayload and internal/poller/executor.go) — matching
// those field names means Central's EXISTING "administrar remotamente"
// screens work against this Windows agent with zero changes on the
// Central side.
type Job struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"` // "files" | "database"
	Name    string `json:"name"`
	Cron    string `json:"cron"`
	Enabled bool   `json:"enabled"`

	// --- files fields ---
	Source           string   `json:"source_path,omitempty"`
	DeleteExtraneous bool     `json:"delete_extraneous,omitempty"`
	Excludes         []string `json:"excludes,omitempty"`

	// Credentials for a network (SMB/UNC) source or destination, used
	// because this agent runs as a service and therefore cannot see a
	// drive letter some logged-in user mapped (see netauth_windows.go).
	//
	// NetworkPasswordEnc is DPAPI-protected at machine scope and is the
	// ONLY form the password ever takes on disk -- there is deliberately
	// no plaintext field, so no code path can accidentally serialize one
	// into jobs.json. It must also never be echoed back to Central: the
	// edit form follows the project's blank-keeps-the-secret convention.
	NetworkUser        string `json:"network_user,omitempty"`
	NetworkPasswordEnc string `json:"network_password_enc,omitempty"`

	// --- database fields ---
	Engine        string `json:"engine,omitempty"`
	Host          string `json:"host,omitempty"`
	Port          int    `json:"port,omitempty"`
	Username      string `json:"username,omitempty"`
	Database      string `json:"database,omitempty"`
	SQLitePath    string `json:"sqlite_path,omitempty"`
	Compression   string `json:"compression,omitempty"`
	RetentionDays int    `json:"retention_days,omitempty"`

	// DBPasswordEnc is the database password, DPAPI-protected at machine
	// scope exactly like NetworkPasswordEnc (and, as there, with no
	// plaintext twin so nothing can serialize one into jobs.json).
	//
	// A MANUAL run gets its password in the command payload, typed by
	// whoever clicked. A SCHEDULED run has nobody to type it, so the
	// secret has to be recoverable by the service alone -- that is the
	// unavoidable cost of unattended execution, and the same tradeoff
	// already accepted for network credentials. The payload password
	// still wins when present, so a one-off run can use a different
	// credential without overwriting the stored one.
	DBPasswordEnc string `json:"db_password_enc,omitempty"`

	// --- destination (shared by both kinds) ---
	Dest     string `json:"destination_path"`
	DestType string `json:"destination_type"` // local|nas|ssh
	SSHHost  string `json:"ssh_host,omitempty"`
	SSHPort  int    `json:"ssh_port,omitempty"`
	SSHUser  string `json:"ssh_user,omitempty"`
	SSHKey   string `json:"ssh_key_path,omitempty"`

	// Not part of the wire payload — purely local bookkeeping for the
	// heartbeat's JobInfo.LastStatus and for `list-jobs`/`status`.
	LastStatus   string    `json:"last_status,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
	LastRunAt    time.Time `json:"last_run_at,omitempty"`
	BytesLastRun int64     `json:"bytes_last_run,omitempty"`
	FilesLastRun int       `json:"files_last_run,omitempty"`
}

type jobStoreFile struct {
	Jobs    []Job          `json:"jobs"`
	History []HistoryEntry `json:"history,omitempty"`
}

// HistoryEntry is one finished (success/failed/cancelled) job execution,
// appended by UpdateStatus — mirrors the Linux agent's job_executions
// table just enough to feed the Central dashboard's per-server timeline
// chart (JobHistory/"job_history" command).
type HistoryEntry struct {
	JobID      string    `json:"job_id"`
	JobName    string    `json:"job_name"`
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Error      string    `json:"error,omitempty"`
}

// maxHistoryEntries caps jobs.json's growth — a timeline chart only ever
// needs a bounded recent window, not an unbounded audit log.
const maxHistoryEntries = 2000

// JobStore is a small mutex-protected JSON-file-backed CRUD store.
type JobStore struct {
	mu   sync.Mutex
	path string
}

func jobsPath() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = "."
	}
	return filepath.Join(base, "GoBackup", "jobs.json")
}

func NewJobStore() *JobStore {
	return &JobStore{path: jobsPath()}
}

func (s *JobStore) load() (jobStoreFile, error) {
	var f jobStoreFile
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return f, err
	}
	if len(data) == 0 {
		return f, nil
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("parse jobs.json: %w", err)
	}
	return f, nil
}

func (s *JobStore) save(f jobStoreFile) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

// Create inserts a new job (or overwrites an existing one with the same
// Name+Kind, matching the Linux Store's "ON CONFLICT ... reuse by name"
// convenience for re-submitting a form with the same job name).
func (s *JobStore) Create(j Job) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return Job{}, err
	}
	for i, existing := range f.Jobs {
		if existing.Name == j.Name && existing.Kind == j.Kind {
			j.ID = existing.ID
			j.LastStatus = existing.LastStatus
			j.LastError = existing.LastError
			j.LastRunAt = existing.LastRunAt
			f.Jobs[i] = j
			return j, s.save(f)
		}
	}
	j.ID = uuid.NewString()
	// Enabled is whatever the caller asked for. It used to be forced to
	// true here, which was harmless while nothing read the field -- but
	// the scheduler honours it, so forcing it would silently re-enable a
	// job the operator had deliberately switched off, and then run it.
	f.Jobs = append(f.Jobs, j)
	return j, s.save(f)
}

func (s *JobStore) Get(id string) (Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return Job{}, false, err
	}
	for _, j := range f.Jobs {
		if j.ID == id {
			return j, true, nil
		}
	}
	return Job{}, false, nil
}

func (s *JobStore) GetByName(name string) (Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return Job{}, false, err
	}
	for _, j := range f.Jobs {
		if j.Name == name {
			return j, true, nil
		}
	}
	return Job{}, false, nil
}

func (s *JobStore) List() ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	return f.Jobs, nil
}

func (s *JobStore) Update(id string, j Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	for i, existing := range f.Jobs {
		if existing.ID == id {
			j.ID = id
			j.LastStatus = existing.LastStatus
			j.LastError = existing.LastError
			j.LastRunAt = existing.LastRunAt
			f.Jobs[i] = j
			return s.save(f)
		}
	}
	return fmt.Errorf("job not found: %s", id)
}

// UpdateStatus patches just the run-result fields, leaving the job
// definition itself untouched — called after every execution. Also
// appends a HistoryEntry (job name snapshotted at call time, since a job
// can be renamed/deleted later but its history should still read right)
// for the Central dashboard's per-server timeline chart.
func (s *JobStore) UpdateStatus(id, status, errMsg string, bytesWritten int64, filesWritten int, startedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	finishedAt := time.Now()
	for i, existing := range f.Jobs {
		if existing.ID == id {
			f.Jobs[i].LastStatus = status
			f.Jobs[i].LastError = errMsg
			f.Jobs[i].LastRunAt = finishedAt
			f.Jobs[i].BytesLastRun = bytesWritten
			f.Jobs[i].FilesLastRun = filesWritten
			f.History = append(f.History, HistoryEntry{
				JobID: id, JobName: f.Jobs[i].Name, Status: status,
				StartedAt: startedAt, FinishedAt: finishedAt, Error: errMsg,
			})
			if len(f.History) > maxHistoryEntries {
				f.History = f.History[len(f.History)-maxHistoryEntries:]
			}
			return s.save(f)
		}
	}
	return fmt.Errorf("job not found: %s", id)
}

// MarkRunning flags a job as currently executing, WITHOUT appending a
// history entry or touching the previous run's result figures.
//
// It is deliberately not UpdateStatus("running", ...): that appends a
// HistoryEntry with a FinishedAt of "now", which would put a bogus
// zero-length bar on the dashboard timeline for a job that has merely
// started. Only terminal states belong in history.
func (s *JobStore) MarkRunning(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	for i, existing := range f.Jobs {
		if existing.ID == id {
			f.Jobs[i].LastStatus = "running"
			f.Jobs[i].LastError = ""
			return s.save(f)
		}
	}
	return fmt.Errorf("job not found: %s", id)
}

// ResetStaleRunning clears any job left marked "running" from a previous
// process lifetime, and reports how many it fixed.
//
// Nothing survives a crash or a service restart mid-backup: the goroutine
// that would have written a terminal status is gone, but the "running"
// flag persisted in jobs.json. Left alone, Central would show that job as
// executing forever AND the UI would keep its "Executar" button disabled
// permanently — a stuck flag that no amount of retrying clears. Must be
// called once at startup, before the first heartbeat.
func (s *JobStore) ResetStaleRunning() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return 0, err
	}
	fixed := 0
	for i, j := range f.Jobs {
		if j.LastStatus == "running" {
			f.Jobs[i].LastStatus = "failed"
			f.Jobs[i].LastError = "interrompida: o agente foi reiniciado durante a execução"
			fixed++
		}
	}
	if fixed == 0 {
		return 0, nil
	}
	return fixed, s.save(f)
}

// ListHistory returns every history entry recorded since the given time,
// oldest first.
func (s *JobStore) ListHistory(since time.Time) ([]HistoryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]HistoryEntry, 0, len(f.History))
	for _, h := range f.History {
		if h.StartedAt.Before(since) {
			continue
		}
		out = append(out, h)
	}
	return out, nil
}

func (s *JobStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	out := f.Jobs[:0]
	found := false
	for _, j := range f.Jobs {
		if j.ID == id {
			found = true
			continue
		}
		out = append(out, j)
	}
	if !found {
		return fmt.Errorf("job not found: %s", id)
	}
	f.Jobs = out
	return s.save(f)
}
