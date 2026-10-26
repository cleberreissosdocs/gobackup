// Package progress is a small in-process registry of "what's running right
// now" per job, shared by the HTTP handlers (for the local polling
// endpoint) and the heartbeat loop (to piggyback onto the Central, same
// mechanism already used for the job list).
package progress

import (
	"sync"
	"time"
)

// State is a snapshot of one job's current/last execution.
type State struct {
	ExecutionID string
	Status      string // "running" | "success" | "failed"
	Percent     int    // 0-100, or -1 when indeterminate (e.g. database dumps)
	Message     string
	UpdatedAt   time.Time
}

// staleAfter: how long a finished (success/failed) entry stays visible
// before being treated as expired — long enough for a browser poll or a
// heartbeat tick to see the final state, short enough not to leak forever.
const staleAfter = 30 * time.Second

type Registry struct {
	mu    sync.Mutex
	byJob map[string]State
}

func NewRegistry() *Registry {
	return &Registry{byJob: map[string]State{}}
}

func (r *Registry) Set(jobID string, s State) {
	s.UpdatedAt = time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byJob[jobID] = s
}

// Get returns the current state for a job, if present and not stale.
func (r *Registry) Get(jobID string) (State, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.byJob[jobID]
	if !ok {
		return State{}, false
	}
	if s.Status != "running" && time.Since(s.UpdatedAt) > staleAfter {
		delete(r.byJob, jobID)
		return State{}, false
	}
	return s, true
}

// Snapshot returns every non-stale entry — used by the heartbeat loop to
// piggyback current progress onto the poll, the same way the job list is.
func (r *Registry) Snapshot() map[string]State {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]State, len(r.byJob))
	for jobID, s := range r.byJob {
		if s.Status != "running" && time.Since(s.UpdatedAt) > staleAfter {
			delete(r.byJob, jobID)
			continue
		}
		out[jobID] = s
	}
	return out
}
