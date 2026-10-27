// Package main (executor.go): handles the SAME remote command protocol the
// Linux agent's internal/poller.Executor implements (create_job, get_job,
// update_job, delete_job, list_jobs, run_job) — matching those command
// types and payload field names means the Central's EXISTING "administrar
// remotamente" screens (internal/webui.CentralRunner) work against this
// Windows agent completely unmodified; Central has no idea the agent on
// the other end isn't the Linux one.
package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/cleber/gobackup/internal/poller"
)

// progress registry — mirrors internal/progress.Registry's job on Linux,
// just local to this process since there's no shared web UI reading it.
type runState struct {
	Status  string
	Percent int
	Message string
}

type progressRegistry struct {
	mu sync.Mutex
	m  map[string]runState
}

func newProgressRegistry() *progressRegistry {
	return &progressRegistry{m: map[string]runState{}}
}

func (p *progressRegistry) set(jobID string, s runState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m[jobID] = s
}

func (p *progressRegistry) snapshot() map[string]runState {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]runState, len(p.m))
	for k, v := range p.m {
		out[k] = v
	}
	return out
}

type Executor struct {
	Store    *JobStore
	Registry *progressRegistry
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

func (e *Executor) dispatch(cmd poller.QueuedCommand) (map[string]any, error) {
	switch cmd.Type {
	case "create_job":
		kind := payloadStr(cmd.Payload, "kind")
		if kind != "" && kind != "files" {
			return nil, fmt.Errorf("gobackup-winagent only supports job kind \"files\" so far (got %q)", kind)
		}
		name := payloadStr(cmd.Payload, "name")
		source := payloadStr(cmd.Payload, "source_path")
		dest := payloadStr(cmd.Payload, "destination_path")
		if name == "" || source == "" || dest == "" {
			return nil, fmt.Errorf("create_job requires name, source_path, destination_path")
		}
		j := FileJob{
			Name: name, Source: source, Dest: dest,
			DestType:         defaultStr(payloadStr(cmd.Payload, "destination_type"), "local"),
			DeleteExtraneous: payloadBool(cmd.Payload, "delete_extraneous", false),
			SSHHost:          payloadStr(cmd.Payload, "ssh_host"),
			SSHPort:          payloadInt(cmd.Payload, "ssh_port", 22),
			SSHUser:          payloadStr(cmd.Payload, "ssh_user"),
			SSHKey:           payloadStr(cmd.Payload, "ssh_key_path"),
			Cron:             payloadStr(cmd.Payload, "cron"),
			Enabled:          true,
		}
		saved, err := e.Store.Create(j)
		if err != nil {
			return nil, err
		}
		return map[string]any{"job_id": saved.ID, "kind": "files"}, nil

	case "get_job":
		id := payloadStr(cmd.Payload, "job_id")
		j, found, err := e.Store.Get(id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("job not found: %s", id)
		}
		return map[string]any{
			"kind": "files", "name": j.Name, "source_path": j.Source, "destination_path": j.Dest,
			"backup_type": "full", "link_dest": "", "delete_extraneous": j.DeleteExtraneous, "destination_type": j.DestType,
			"ssh_host": j.SSHHost, "ssh_port": j.SSHPort, "ssh_user": j.SSHUser, "ssh_key_path": j.SSHKey,
			"cron": j.Cron, "enabled": j.Enabled,
		}, nil

	case "update_job":
		id := payloadStr(cmd.Payload, "job_id")
		existing, found, err := e.Store.Get(id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("job not found: %s", id)
		}
		existing.Name = payloadStr(cmd.Payload, "name")
		existing.Source = payloadStr(cmd.Payload, "source_path")
		existing.Dest = payloadStr(cmd.Payload, "destination_path")
		existing.DestType = defaultStr(payloadStr(cmd.Payload, "destination_type"), "local")
		existing.DeleteExtraneous = payloadBool(cmd.Payload, "delete_extraneous", false)
		existing.SSHHost = payloadStr(cmd.Payload, "ssh_host")
		existing.SSHPort = payloadInt(cmd.Payload, "ssh_port", 22)
		existing.SSHUser = payloadStr(cmd.Payload, "ssh_user")
		existing.SSHKey = payloadStr(cmd.Payload, "ssh_key_path")
		existing.Cron = payloadStr(cmd.Payload, "cron")
		existing.Enabled = payloadBool(cmd.Payload, "enabled", true)
		if err := e.Store.Update(id, existing); err != nil {
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
		}
		list := make([]jobEntry, 0, len(jobs))
		for _, j := range jobs {
			list = append(list, jobEntry{ID: j.ID, Name: j.Name, Kind: "files", Cron: j.Cron, Enabled: j.Enabled, LastStatus: j.LastStatus})
		}
		return map[string]any{"jobs": list}, nil

	case "run_job":
		id := payloadStr(cmd.Payload, "job_id")
		j, found, err := e.Store.Get(id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("job not found: %s", id)
		}
		go e.runFileJob(j)
		return map[string]any{"job_id": id, "status": "started"}, nil

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

// runFileJob actually performs the copy — started in its own goroutine so
// a multi-minute backup never blocks the 1s heartbeat tick, the same
// design the Linux agent's jobexec.Engine uses.
func (e *Executor) runFileJob(j FileJob) {
	e.Registry.set(j.ID, runState{Status: "running", Percent: 0, Message: "iniciando..."})

	if j.DestType == "ssh" {
		errMsg := "destino SSH ainda não é suportado no agente Windows (somente local/NAS por enquanto)"
		e.Registry.set(j.ID, runState{Status: "failed", Percent: -1, Message: errMsg})
		_ = e.Store.UpdateStatus(j.ID, "failed", errMsg, 0, 0)
		return
	}

	lastUpdate := time.Now()
	result, err := Mirror(j.Source, j.Dest, j.DeleteExtraneous, func(filesDone, filesTotal int, bytesDone int64, currentFile string) {
		if time.Since(lastUpdate) < 200*time.Millisecond && filesDone != filesTotal {
			return
		}
		lastUpdate = time.Now()
		pct := 0
		if filesTotal > 0 {
			pct = filesDone * 100 / filesTotal
		} else {
			pct = 100
		}
		e.Registry.set(j.ID, runState{Status: "running", Percent: pct, Message: fmt.Sprintf("%d/%d arquivos — %s", filesDone, filesTotal, currentFile)})
	})

	if err != nil {
		e.Registry.set(j.ID, runState{Status: "failed", Percent: -1, Message: err.Error()})
		_ = e.Store.UpdateStatus(j.ID, "failed", err.Error(), result.BytesCopied, result.FilesCopied)
		return
	}

	msg := fmt.Sprintf("%d arquivo(s) copiado(s), %d pulado(s) (já atualizados), %d byte(s)", result.FilesCopied, result.FilesSkipped, result.BytesCopied)
	if result.FilesDeleted > 0 {
		msg += fmt.Sprintf(", %d removido(s) do destino", result.FilesDeleted)
	}
	e.Registry.set(j.ID, runState{Status: "success", Percent: 100, Message: msg})
	_ = e.Store.UpdateStatus(j.ID, "success", "", result.BytesCopied, result.FilesCopied)
}
