// Package winagentcore (heartbeat.go): the single heartbeat engine shared
// by `gobackup-winagent serve` (headless, for servers) and the tray app
// (desktop, shows live status) — one implementation, so the two can never
// drift out of sync in how they talk to the Central.
package winagentcore

import (
	"fmt"
	"log"
	"time"

	"github.com/cleber/gobackup/internal/poller"
)

// Agent bundles everything one heartbeat cycle needs: the job store, the
// in-flight progress registry, and a cached poller.Client (rebuilt only
// when the saved Config actually changes).
type Agent struct {
	Store    *JobStore
	Registry *ProgressRegistry
	Executor *Executor

	client   *poller.Client
	clientOf string // cache key: CentralURL|Token|CACertPEM
}

func NewAgent() *Agent {
	store := NewJobStore()
	registry := NewProgressRegistry()
	return &Agent{
		Store:    store,
		Registry: registry,
		Executor: &Executor{Store: store, Registry: registry},
	}
}

// TickResult reports what happened during one heartbeat.
type TickResult struct {
	Configured    bool
	Connected     bool
	Err           error
	CommandsCount int
}

// Tick performs exactly one heartbeat round-trip against the Central: load
// config, (re)build the client if needed, poll, execute any returned
// commands, and report the outcome. Safe to call on a plain ticker loop —
// each call is self-contained (no goroutines started).
func (a *Agent) Tick(pendingResults *[]poller.CommandResult) TickResult {
	cfg, err := LoadConfig()
	if err != nil {
		return TickResult{Err: fmt.Errorf("erro lendo config: %w", err)}
	}
	if !cfg.IsConfigured() {
		return TickResult{Configured: false}
	}

	key := cfg.CentralURL + "|" + cfg.Token + "|" + cfg.CACertPEM
	if a.client == nil || key != a.clientOf {
		client, err := poller.NewClient(cfg.CentralURL, cfg.Token, []byte(cfg.CACertPEM))
		if err != nil {
			return TickResult{Configured: true, Err: fmt.Errorf("erro criando cliente: %w", err)}
		}
		a.client = client
		a.clientOf = key
	}

	jobs, _ := a.Store.List()
	jobInfos := make([]poller.JobInfo, 0, len(jobs))
	for _, j := range jobs {
		jobInfos = append(jobInfos, poller.JobInfo{ID: j.ID, Name: j.Name, Kind: j.Kind, Cron: j.Cron, Enabled: j.Enabled, LastStatus: j.LastStatus})
	}
	running := make([]poller.RunningInfo, 0)
	for jobID, s := range a.Registry.Snapshot() {
		running = append(running, poller.RunningInfo{JobID: jobID, Status: s.Status, Percent: s.Percent, Message: s.Message})
	}

	resp, err := a.client.Poll("idle", *pendingResults, jobInfos, running)
	*pendingResults = nil
	if err != nil {
		return TickResult{Configured: true, Connected: false, Err: err}
	}

	for _, cmd := range resp.Commands {
		*pendingResults = append(*pendingResults, a.Executor.Execute(cmd))
	}
	return TickResult{Configured: true, Connected: true, CommandsCount: len(resp.Commands)}
}

// RunLoop ticks forever at the given interval, calling onTick after every
// attempt — used by `serve` (prints to stdout) and the tray app (updates
// the icon/menu). Blocks until stop is closed.
func RunLoop(interval time.Duration, onTick func(TickResult), stop <-chan struct{}) {
	agent := NewAgent()
	// Clear any job still flagged "running" from a previous process
	// lifetime BEFORE the first heartbeat, so Central never sees a job
	// that died with the service reported as still executing (which would
	// also keep its "Executar" button disabled forever).
	if n, err := agent.Store.ResetStaleRunning(); err == nil && n > 0 {
		log.Printf("gobackup: %d tarefa(s) estavam marcadas como em execução de uma sessão anterior; marcadas como interrompidas", n)
	}
	var pending []poller.CommandResult
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		onTick(agent.Tick(&pending))
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}
