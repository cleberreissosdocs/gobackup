package poller

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/cleber/gobackup/internal/db"
	"github.com/cleber/gobackup/internal/jobexec"
	"github.com/cleber/gobackup/internal/progress"
)

// Runner drives the heartbeat/poll loop using settings that can change at
// runtime (central URL, CA cert, token) — read fresh from agent_settings on
// every tick, so updating them from the web UI takes effect on the very
// next beat without restarting the process.
type Runner struct {
	Store    *db.Store
	TenantID uuid.UUID
	AgentID  uuid.UUID
	Engine   *jobexec.Engine    // shared with the local web UI and the scheduler
	Registry *progress.Registry // shared with the local web UI's status endpoint

	mu            sync.Mutex
	cachedKey     string
	cachedClient  *Client
	lastBeatOK    bool
	lastBeatAt    time.Time
	lastBeatError string
}

const (
	settingCentralURL = "central_url"
	settingCentralCA  = "central_ca_pem"
	settingToken      = "central_token"
)

// Configure persists the Central connection settings (called by the
// Settings page after a successful registration).
func (r *Runner) Configure(ctx context.Context, centralURL, token string, caPEM []byte) error {
	if err := r.Store.SetSetting(ctx, settingCentralURL, centralURL); err != nil {
		return err
	}
	if err := r.Store.SetSetting(ctx, settingToken, token); err != nil {
		return err
	}
	return r.Store.SetSetting(ctx, settingCentralCA, string(caPEM))
}

// Status reports the current configuration + last heartbeat outcome, for
// the Settings page to display.
type Status struct {
	CentralURL    string
	Configured    bool
	LastBeatOK    bool
	LastBeatAt    time.Time
	LastBeatError string
}

func (r *Runner) Status(ctx context.Context) Status {
	centralURL, _, _ := r.Store.GetSetting(ctx, settingCentralURL)
	token, _, _ := r.Store.GetSetting(ctx, settingToken)

	r.mu.Lock()
	defer r.mu.Unlock()
	return Status{
		CentralURL:    centralURL,
		Configured:    centralURL != "" && token != "",
		LastBeatOK:    r.lastBeatOK,
		LastBeatAt:    r.lastBeatAt,
		LastBeatError: r.lastBeatError,
	}
}

// Run loops forever (until ctx is cancelled), sending a heartbeat every
// interval. Call it in a goroutine from `gobackup-agent serve`. Commands
// from the Central (including run_job) are handled by Executor, which
// dispatches long-running work to jobexec.Engine so a single heartbeat
// tick never blocks on a multi-minute backup.
func (r *Runner) Run(ctx context.Context, interval time.Duration) {
	exec := &Executor{Store: r.Store, TenantID: r.TenantID, AgentID: r.AgentID, Engine: r.Engine, Registry: r.Registry}
	var pendingResults []CommandResult

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		client, ok := r.clientFor(ctx)
		if ok {
			jobs, _ := r.Store.ListJobs(ctx, r.AgentID)
			jobInfos := make([]JobInfo, 0, len(jobs))
			for _, j := range jobs {
				jobInfos = append(jobInfos, JobInfo{ID: j.ID.String(), Name: j.Name, Kind: j.Kind, Cron: j.Cron, Enabled: j.Enabled, LastStatus: j.LastStatus})
			}

			running := make([]RunningInfo, 0)
			for jobID, s := range r.Registry.Snapshot() {
				running = append(running, RunningInfo{JobID: jobID, Status: s.Status, Percent: s.Percent, Message: s.Message})
			}

			resp, err := client.Poll("idle", pendingResults, jobInfos, running)
			r.mu.Lock()
			r.lastBeatAt = time.Now()
			if err != nil {
				r.lastBeatOK = false
				r.lastBeatError = err.Error()
				r.mu.Unlock()
				pendingResults = nil
			} else {
				r.lastBeatOK = true
				r.lastBeatError = ""
				r.mu.Unlock()
				pendingResults = nil
				for _, cmd := range resp.Commands {
					log.Printf("executing command %s (%s)", cmd.ID, cmd.Type)
					result := exec.Execute(ctx, cmd)
					pendingResults = append(pendingResults, result)
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// clientFor returns a Client built from the current settings, rebuilding it
// only when the settings actually changed since the last tick (so TLS
// connections get reused across the very frequent 1s-interval beats).
func (r *Runner) clientFor(ctx context.Context) (*Client, bool) {
	centralURL, _, _ := r.Store.GetSetting(ctx, settingCentralURL)
	token, _, _ := r.Store.GetSetting(ctx, settingToken)
	caPEM, _, _ := r.Store.GetSetting(ctx, settingCentralCA)
	if centralURL == "" || token == "" {
		return nil, false
	}

	key := centralURL + "|" + token + "|" + caPEM
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cachedClient != nil && r.cachedKey == key {
		return r.cachedClient, true
	}

	client, err := NewClient(centralURL, token, []byte(caPEM))
	if err != nil {
		r.lastBeatError = err.Error()
		return nil, false
	}
	r.cachedClient = client
	r.cachedKey = key
	return client, true
}
