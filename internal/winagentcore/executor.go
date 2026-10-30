// Package main (executor.go): handles the SAME remote command protocol the
// Linux agent's internal/poller.Executor implements (create_job, get_job,
// update_job, delete_job, list_jobs, run_job) for BOTH job kinds (files and
// database) — matching those command types and payload field names means
// the Central's EXISTING "administrar remotamente" screens
// (internal/webui.CentralRunner) work against this Windows agent
// completely unmodified; Central has no idea the agent on the other end
// isn't the Linux one.
package winagentcore

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cleber/gobackup/internal/core"
	"github.com/cleber/gobackup/internal/poller"
)

// progress registry — mirrors internal/progress.Registry's job on Linux,
// just local to this process since there's no shared web UI reading it.
type RunState struct {
	Status  string
	Percent int
	Message string
}

type ProgressRegistry struct {
	mu sync.Mutex
	m  map[string]RunState
}

func NewProgressRegistry() *ProgressRegistry {
	return &ProgressRegistry{m: map[string]RunState{}}
}

func (p *ProgressRegistry) Set(jobID string, s RunState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m[jobID] = s
}

func (p *ProgressRegistry) Get(jobID string) (RunState, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.m[jobID]
	return s, ok
}

func (p *ProgressRegistry) Snapshot() map[string]RunState {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]RunState, len(p.m))
	for k, v := range p.m {
		out[k] = v
	}
	return out
}

type Executor struct {
	Store    *JobStore
	Registry *ProgressRegistry

	mu       sync.Mutex
	controls map[string]*JobControl
}

func payloadStr(payload map[string]any, key string) string {
	v, _ := payload[key].(string)
	return v
}

func payloadBool(payload map[string]any, key string, def bool) bool {
	if b, ok := payload[key].(bool); ok {
		return b
	}
	return def
}

func payloadInt(payload map[string]any, key string, def int) int {
	if f, ok := payload[key].(float64); ok {
		return int(f)
	}
	return def
}

// payloadStrings reads a JSON array of strings out of a command payload
// (it arrives as []any after json.Unmarshal, never []string). Also accepts
// a single newline-separated string, so an older Central that still sends
// the raw textarea text keeps working.
func payloadStrings(payload map[string]any, key string) []string {
	switch v := payload[key].(type) {
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		return core.ParseExcludePatterns(v)
	default:
		return nil
	}
}

// Execute dispatches one queued command, returning the result to piggyback
// on the next heartbeat — the exact same "acked"/"failed" shape the Linux
// executor returns.
func (e *Executor) Execute(cmd poller.QueuedCommand) poller.CommandResult {
	result, err := e.dispatch(cmd)
	if err != nil {
		return poller.CommandResult{CommandID: cmd.ID, Status: "failed", Result: map[string]any{"error": err.Error()}}
	}
	return poller.CommandResult{CommandID: cmd.ID, Status: "acked", Result: result}
}

// encryptOrEmpty protects a secret for storage, returning "" when there
// is nothing to store OR when encryption fails. It deliberately has no
// plaintext fallback: a job whose credential failed to encrypt must fail
// later with a clear "credential could not be read" error rather than
// quietly persisting a readable password in jobs.json.
func encryptOrEmpty(plaintext string) string {
	enc, err := EncryptSecret(plaintext)
	if err != nil {
		return ""
	}
	return enc
}

func jobFromFilePayload(payload map[string]any) Job {
	return Job{
		Kind:             "files",
		Name:             payloadStr(payload, "name"),
		Source:           NormalizeWindowsPath(payloadStr(payload, "source_path")),
		Dest:             NormalizeDestPath(payloadStr(payload, "destination_path"), payloadStr(payload, "destination_type")),
		DestType:         defaultStr(payloadStr(payload, "destination_type"), "local"),
		DeleteExtraneous: payloadBool(payload, "delete_extraneous", false),
		Excludes:         payloadStrings(payload, "excludes"),
		NetworkUser:      strings.TrimSpace(payloadStr(payload, "network_user")),
		// Encrypted right here at the boundary, so the plaintext never
		// reaches the Job struct that gets serialized to jobs.json. An
		// encryption failure is swallowed into an empty value rather than
		// storing plaintext as a fallback -- connectJobShares then fails
		// loudly with a credential error, which is the safe direction.
		NetworkPasswordEnc: encryptOrEmpty(payloadStr(payload, "network_password")),
		SSHHost:            payloadStr(payload, "ssh_host"),
		SSHPort:            payloadInt(payload, "ssh_port", 22),
		SSHUser:            payloadStr(payload, "ssh_user"),
		SSHKey:             payloadStr(payload, "ssh_key_path"),
		Cron:               payloadStr(payload, "cron"),
		Enabled:            payloadBool(payload, "enabled", true),
	}
}

func jobFromDBPayload(payload map[string]any) Job {
	return Job{
		Kind:          "database",
		Name:          payloadStr(payload, "name"),
		Engine:        payloadStr(payload, "engine"),
		Host:          payloadStr(payload, "host"),
		Port:          payloadInt(payload, "port", 0),
		Username:      payloadStr(payload, "username"),
		Database:      payloadStr(payload, "database"),
		SQLitePath:    NormalizeWindowsPath(payloadStr(payload, "sqlite_path")),
		Compression:   defaultStr(payloadStr(payload, "compression"), "gzip"),
		Dest:          NormalizeDestPath(payloadStr(payload, "destination_path"), payloadStr(payload, "destination_type")),
		DestType:      defaultStr(payloadStr(payload, "destination_type"), "local"),
		SSHHost:       payloadStr(payload, "ssh_host"),
		SSHPort:       payloadInt(payload, "ssh_port", 22),
		SSHUser:       payloadStr(payload, "ssh_user"),
		SSHKey:        payloadStr(payload, "ssh_key_path"),
		RetentionDays: payloadInt(payload, "retention_days", 0),
		Cron:          payloadStr(payload, "cron"),
		Enabled:       payloadBool(payload, "enabled", true),
	}
}

// NormalizeDestPath only runs the Windows path fixups for a local/NAS
// destination — an "ssh" destination's path is a REMOTE (typically Linux)
// path and must be left exactly as typed (forward slashes, no drive
// letter concept), not mangled by Windows-specific normalization.
func NormalizeDestPath(path, destType string) string {
	if destType == "ssh" || path == "" {
		return path
	}
	return NormalizeWindowsPath(path)
}

func fileJobResult(j Job) map[string]any {
	return map[string]any{
		"kind": "files", "name": j.Name, "source_path": j.Source, "destination_path": j.Dest,
		"backup_type": "full", "link_dest": "", "delete_extraneous": j.DeleteExtraneous,
		"excludes": j.Excludes, "destination_type": j.DestType,
		// network_user round-trips so the edit form can prefill it;
		// the password deliberately does NOT -- a stored secret is never
		// rendered back into a page (blank means "keep the current one").
		// has_network_password lets the form say whether one is saved
		// without revealing it.
		"network_user": j.NetworkUser, "has_network_password": j.NetworkPasswordEnc != "",
		"ssh_host": j.SSHHost, "ssh_port": j.SSHPort, "ssh_user": j.SSHUser, "ssh_key_path": j.SSHKey,
		"cron": j.Cron, "enabled": j.Enabled,
	}
}

func dbJobResult(j Job) map[string]any {
	return map[string]any{
		"kind": "database", "name": j.Name, "engine": j.Engine, "host": j.Host, "port": j.Port,
		"username": j.Username, "database": j.Database, "sqlite_path": j.SQLitePath, "compression": j.Compression,
		"destination_path": j.Dest, "destination_type": j.DestType,
		"ssh_host": j.SSHHost, "ssh_port": j.SSHPort, "ssh_user": j.SSHUser, "ssh_key_path": j.SSHKey,
		"retention_days": j.RetentionDays, "cron": j.Cron, "enabled": j.Enabled,
	}
}

func (e *Executor) dispatch(cmd poller.QueuedCommand) (map[string]any, error) {
	switch cmd.Type {
	case "list_dir":
		return listDir(cmd.Payload)

	case "create_job":
		kind := payloadStr(cmd.Payload, "kind")
		switch kind {
		case "", "files":
			j := jobFromFilePayload(cmd.Payload)
			if j.Name == "" || j.Source == "" || j.Dest == "" {
				return nil, fmt.Errorf("create_job requires name, source_path, destination_path")
			}
			saved, err := e.Store.Create(j)
			if err != nil {
				return nil, err
			}
			return map[string]any{"job_id": saved.ID, "kind": "files"}, nil
		case "database":
			j := jobFromDBPayload(cmd.Payload)
			if j.Name == "" || j.Engine == "" || j.Dest == "" {
				return nil, fmt.Errorf("create_job requires name, engine, destination_path")
			}
			saved, err := e.Store.Create(j)
			if err != nil {
				return nil, err
			}
			return map[string]any{"job_id": saved.ID, "kind": "database"}, nil
		default:
			return nil, fmt.Errorf("unsupported job kind: %q", kind)
		}

	case "get_job":
		id := payloadStr(cmd.Payload, "job_id")
		j, found, err := e.Store.Get(id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("job not found: %s", id)
		}
		if j.Kind == "database" {
			return dbJobResult(j), nil
		}
		return fileJobResult(j), nil

	case "update_job":
		id := payloadStr(cmd.Payload, "job_id")
		existing, found, err := e.Store.Get(id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("job not found: %s", id)
		}
		var updated Job
		if existing.Kind == "database" {
			updated = jobFromDBPayload(cmd.Payload)
		} else {
			updated = jobFromFilePayload(cmd.Payload)
		}
		updated.Kind = existing.Kind
		// Blank password on an edit means "keep the current one" -- the
		// same convention the notification channels use, and the reason
		// the form never renders a stored secret back into the page.
		// Without this, merely opening a job and pressing save would
		// silently wipe its network credential.
		if payloadStr(cmd.Payload, "network_password") == "" {
			updated.NetworkPasswordEnc = existing.NetworkPasswordEnc
		}
		if err := e.Store.Update(id, updated); err != nil {
			return nil, err
		}
		return map[string]any{"job_id": id}, nil

	case "delete_job":
		id := payloadStr(cmd.Payload, "job_id")
		if err := e.Store.Delete(id); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true}, nil

	case "list_jobs":
		jobs, err := e.Store.List()
		if err != nil {
			return nil, err
		}
		type jobEntry struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Kind       string `json:"kind"`
			Cron       string `json:"cron"`
			Enabled    bool   `json:"enabled"`
			LastStatus string `json:"last_status"`
			Percent    int    `json:"percent,omitempty"`
		}
		list := make([]jobEntry, 0, len(jobs))
		for _, j := range jobs {
			entry := jobEntry{ID: j.ID, Name: j.Name, Kind: j.Kind, Cron: j.Cron, Enabled: j.Enabled, LastStatus: j.LastStatus}
			if entry.LastStatus == "running" {
				if live, ok := e.Registry.Get(entry.ID); ok {
					entry.Percent = live.Percent
				}
			}
			list = append(list, entry)
		}
		return map[string]any{"jobs": list}, nil

	case "run_job":
		id := payloadStr(cmd.Payload, "job_id")
		password := payloadStr(cmd.Payload, "password")
		j, found, err := e.Store.Get(id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("job not found: %s", id)
		}
		if !e.tryReserve(id) {
			return nil, fmt.Errorf("esta tarefa já está em execução")
		}
		if j.Kind == "database" {
			go e.runDBJob(j, password)
		} else {
			go e.runFileJob(j)
		}
		return map[string]any{"job_id": id, "status": "started"}, nil

	case "cancel_job":
		id := payloadStr(cmd.Payload, "job_id")
		if err := e.CancelJob(id); err != nil {
			return nil, err
		}
		return map[string]any{"job_id": id, "status": "cancelled"}, nil

	case "pause_job":
		id := payloadStr(cmd.Payload, "job_id")
		if err := e.PauseJob(id); err != nil {
			return nil, err
		}
		return map[string]any{"job_id": id, "status": "paused"}, nil

	case "resume_job":
		id := payloadStr(cmd.Payload, "job_id")
		if err := e.ResumeJob(id); err != nil {
			return nil, err
		}
		return map[string]any{"job_id": id, "status": "running"}, nil

	case "job_history":
		days := 30
		if d, ok := cmd.Payload["days"].(float64); ok && d > 0 {
			days = int(d)
		}
		since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
		history, err := e.Store.ListHistory(since)
		if err != nil {
			return nil, err
		}
		type execEntry struct {
			JobID      string `json:"job_id"`
			JobName    string `json:"job_name"`
			Status     string `json:"status"`
			StartedAt  string `json:"started_at"`
			FinishedAt string `json:"finished_at"`
			Error      string `json:"error,omitempty"`
		}
		list := make([]execEntry, 0, len(history))
		for _, h := range history {
			if h.Status != "success" && h.Status != "failed" {
				continue
			}
			list = append(list, execEntry{
				JobID: h.JobID, JobName: h.JobName, Status: h.Status,
				StartedAt: h.StartedAt.Format(time.RFC3339), FinishedAt: h.FinishedAt.Format(time.RFC3339),
				Error: h.Error,
			})
		}
		return map[string]any{"executions": list}, nil

	default:
		return nil, fmt.Errorf("unsupported command type: %s", cmd.Type)
	}
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// connectJobShares authenticates this process to every distinct network
// share the job reads from or writes to, decrypting the stored password
// just in time.
//
// Source and destination are handled separately because a job may legally
// pull from one server and push to another; UNCServerRoot returns "" for
// an ordinary local path, which makes ConnectUNC a no-op, so no caller
// needs to pre-check whether a path is a network one.
//
// A stored password that fails to decrypt is a hard error, never a silent
// fallback to an anonymous connection: it means the credential is
// unusable (typically jobs.json copied from another machine, which
// machine-scoped DPAPI refuses by design), and an unauthenticated retry
// would report "access denied" instead of the real cause.
func (e *Executor) connectJobShares(j Job) error {
	if j.NetworkUser == "" {
		return nil
	}
	password, err := DecryptSecret(j.NetworkPasswordEnc)
	if err != nil {
		return fmt.Errorf("credencial de rede da tarefa %q não pôde ser lida: %w", j.Name, err)
	}
	seen := map[string]bool{}
	for _, path := range []string{j.Source, j.Dest} {
		share := UNCServerRoot(path)
		if share == "" || seen[share] {
			continue
		}
		seen[share] = true
		if err := ConnectUNC(path, j.NetworkUser, password); err != nil {
			return err
		}
	}
	return nil
}

// runFileJob actually performs the copy — started in its own goroutine so
// a multi-minute backup never blocks the 1s heartbeat tick, the same
// design the Linux agent's jobexec.Engine uses.
func (e *Executor) runFileJob(j Job) {
	startedAt := time.Now()
	e.Registry.Set(j.ID, RunState{Status: "running", Percent: 0, Message: "iniciando..."})
	// Persist "running" too, not just the in-memory registry: Central
	// reads a job's state from last_status in the list_jobs response, so
	// without this a Windows agent's running job never shows as
	// "Executando" (and its progress page is never linked) no matter how
	// long it runs. The Linux agent gets this for free because it records
	// a job_executions row at start.
	_ = e.Store.MarkRunning(j.ID)

	ctx, jc := e.registerControl(context.Background(), j.ID, "files")
	defer e.unregisterControl(j.ID)

	lastUpdate := time.Now()
	progress := func(filesDone, filesTotal int, bytesDone int64, currentFile string) {
		if time.Since(lastUpdate) < 200*time.Millisecond && filesDone != filesTotal {
			return
		}
		lastUpdate = time.Now()
		pct := 100
		if filesTotal > 0 {
			pct = filesDone * 100 / filesTotal
		}
		e.Registry.Set(j.ID, RunState{Status: "running", Percent: pct, Message: fmt.Sprintf("%d/%d arquivos — %s", filesDone, filesTotal, currentFile)})
	}
	// Must run unconditionally for EVERY file, from every concurrent
	// worker -- unlike progress(), which throttles UI updates to one per
	// 200ms. A job copying hundreds of small files per worker per tick
	// would blow straight through a Pause if the wait were buried inside
	// that throttled callback (confirmed live: Pause accepted but the
	// copy kept progressing to completion).
	waitIfPaused := func(ctx context.Context) {
		if jc.Pause != nil {
			jc.Pause.Wait(ctx)
		}
	}

	// Authenticate to any network share this job touches BEFORE copying.
	// A no-op for ordinary local paths, so this is safe unconditionally.
	// Failing here must abort the job rather than fall through: without
	// credentials the copy would fail anyway, but with a confusing
	// "path not found" instead of naming the real problem.
	if netErr := e.connectJobShares(j); netErr != nil {
		e.Registry.Set(j.ID, RunState{Status: "failed", Percent: -1, Message: netErr.Error()})
		_ = e.Store.UpdateStatus(j.ID, "failed", netErr.Error(), 0, 0, startedAt)
		return
	}

	var result MirrorResult
	var err error
	if j.DestType == "ssh" {
		result, err = MirrorSSH(ctx, j.Source, j.SSHHost, j.SSHPort, j.SSHUser, j.SSHKey, j.Dest, j.DeleteExtraneous, j.Excludes, progress, waitIfPaused)
	} else {
		result, err = Mirror(ctx, j.Source, j.Dest, j.DeleteExtraneous, j.Excludes, progress, waitIfPaused)
	}

	if err != nil {
		if ctx.Err() == context.Canceled {
			e.Registry.Set(j.ID, RunState{Status: "cancelled", Percent: -1, Message: "cancelado pelo usuário"})
			_ = e.Store.UpdateStatus(j.ID, "cancelled", "cancelado pelo usuário", result.BytesCopied, result.FilesCopied, startedAt)
			return
		}
		e.Registry.Set(j.ID, RunState{Status: "failed", Percent: -1, Message: err.Error()})
		_ = e.Store.UpdateStatus(j.ID, "failed", err.Error(), result.BytesCopied, result.FilesCopied, startedAt)
		return
	}

	msg := fmt.Sprintf("%d arquivo(s) copiado(s), %d pulado(s) (já atualizados), %d byte(s)", result.FilesCopied, result.FilesSkipped, result.BytesCopied)
	if result.FilesDeleted > 0 {
		msg += fmt.Sprintf(", %d removido(s) do destino", result.FilesDeleted)
	}
	e.Registry.Set(j.ID, RunState{Status: "success", Percent: 100, Message: msg})
	_ = e.Store.UpdateStatus(j.ID, "success", "", result.BytesCopied, result.FilesCopied, startedAt)
}

// runDBJob dumps the database and ships it to the configured destination.
// password arrives fresh with every run_job command — never persisted in
// jobs.json (matching the Linux agent's behavior for password-protected
// engines).
func (e *Executor) runDBJob(j Job, password string) {
	startedAt := time.Now()
	_ = e.Store.MarkRunning(j.ID)
	e.Registry.Set(j.ID, RunState{Status: "running", Percent: 0, Message: "gerando dump..."})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	ctx, jc := e.registerControl(ctx, j.ID, "database")
	_ = jc
	defer e.unregisterControl(j.ID)

	result, err := RunDBJob(ctx, j, password)
	if err != nil {
		if ctx.Err() == context.Canceled {
			e.Registry.Set(j.ID, RunState{Status: "cancelled", Percent: -1, Message: "cancelado pelo usuário"})
			_ = e.Store.UpdateStatus(j.ID, "cancelled", "cancelado pelo usuário", 0, 0, startedAt)
			return
		}
		e.Registry.Set(j.ID, RunState{Status: "failed", Percent: -1, Message: err.Error()})
		_ = e.Store.UpdateStatus(j.ID, "failed", err.Error(), 0, 0, startedAt)
		return
	}

	msg := fmt.Sprintf("dump gerado: %s (%d byte(s))", result.OutputPath, result.BytesWritten)
	if result.Deleted > 0 {
		msg += fmt.Sprintf(", %d backup(s) antigo(s) removido(s) pela retenção", result.Deleted)
	}
	e.Registry.Set(j.ID, RunState{Status: "success", Percent: 100, Message: msg})
	_ = e.Store.UpdateStatus(j.ID, "success", "", result.BytesWritten, 1, startedAt)
}
