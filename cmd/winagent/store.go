// Package main (store.go): local job persistence for gobackup-winagent.
// There is no Postgres on this platform, so job definitions live in a
// small JSON file instead of a database — perfectly fine at the scale of
// "jobs for one server" this agent manages.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// FileJob mirrors the fields the Central already sends/expects for a
// job_kind="files" job (see internal/webui/central_runner.go's
// fileJobPayload and internal/poller/executor.go's createJob/updateJob) —
// matching that shape means Central's EXISTING "administrar remotamente"
// screens work against this Windows agent with zero changes on the
// Central side.
type FileJob struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Source           string `json:"source_path"`
	Dest             string `json:"destination_path"`
	DestType         string `json:"destination_type"` // local|nas|ssh ("ssh" not yet implemented on Windows)
	DeleteExtraneous bool   `json:"delete_extraneous"`
	SSHHost          string `json:"ssh_host"`
	SSHPort          int    `json:"ssh_port"`
	SSHUser          string `json:"ssh_user"`
	SSHKey           string `json:"ssh_key_path"`
	Cron             string `json:"cron"`
	Enabled          bool   `json:"enabled"`

	// Not part of the wire payload (omitted from create/update/get_job
	// round-trips that only carry the fields above) — purely local
	// bookkeeping for the heartbeat's JobInfo.LastStatus and for `status`.
	LastStatus   string    `json:"last_status,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
	LastRunAt    time.Time `json:"last_run_at,omitempty"`
	BytesLastRun int64     `json:"bytes_last_run,omitempty"`
	FilesLastRun int       `json:"files_last_run,omitempty"`
}

type jobStoreFile struct {
	Jobs []FileJob `json:"jobs"`
}

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
// Name, matching the Linux Store's "ON CONFLICT ... reuse by name"
// convenience for re-submitting a form with the same job name).
func (s *JobStore) Create(j FileJob) (FileJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return FileJob{}, err
	}
	for i, existing := range f.Jobs {
		if existing.Name == j.Name {
			j.ID = existing.ID
			j.LastStatus = existing.LastStatus
			j.LastError = existing.LastError
			j.LastRunAt = existing.LastRunAt
			f.Jobs[i] = j
			return j, s.save(f)
		}
	}
	j.ID = uuid.NewString()
	j.Enabled = true
	f.Jobs = append(f.Jobs, j)
	return j, s.save(f)
}

func (s *JobStore) Get(id string) (FileJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return FileJob{}, false, err
	}
	for _, j := range f.Jobs {
		if j.ID == id {
			return j, true, nil
		}
	}
	return FileJob{}, false, nil
}

func (s *JobStore) List() ([]FileJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	return f.Jobs, nil
}

func (s *JobStore) Update(id string, j FileJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	for i, existing := range f.Jobs {
		if existing.ID == id {
			j.ID = id
			f.Jobs[i] = j
			return s.save(f)
		}
	}
	return fmt.Errorf("job not found: %s", id)
}

// UpdateStatus patches just the run-result fields, leaving the job
// definition itself untouched — called after every execution.
func (s *JobStore) UpdateStatus(id, status, errMsg string, bytesWritten int64, filesWritten int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	for i, existing := range f.Jobs {
		if existing.ID == id {
			f.Jobs[i].LastStatus = status
			f.Jobs[i].LastError = errMsg
			f.Jobs[i].LastRunAt = time.Now()
			f.Jobs[i].BytesLastRun = bytesWritten
			f.Jobs[i].FilesLastRun = filesWritten
			return s.save(f)
		}
	}
	return fmt.Errorf("job not found: %s", id)
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
