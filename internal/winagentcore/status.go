// Package winagentcore (status.go): cross-process status handoff between
// the process that ACTUALLY runs the heartbeat loop (the Windows Service,
// so backups keep running with nobody logged in — see service_windows.go
// in cmd/winagent) and the tray app, which merely displays status for a
// logged-in user. Both read/write the same machine-wide file under
// %ProgramData%, the same way Config and the job store already do, so it
// works regardless of which Windows account each process runs as (SYSTEM
// for the service, the interactive user for the tray).
package winagentcore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// StatusSnapshot is TickResult plus a timestamp, so a reader can tell a
// genuinely fresh "connected" result from a stale one left behind by a
// service that has since stopped (e.g. crashed, or was never installed).
type StatusSnapshot struct {
	TickResult
	UpdatedAt time.Time `json:"updated_at"`
}

func statusPath() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = "."
	}
	return filepath.Join(base, "GoBackup", "status.json")
}

// SaveStatus is called after every heartbeat tick by whichever process is
// actually running the loop (the service in normal operation; `serve`
// when run manually/under a Scheduled Task; never the tray, which only
// reads this). Best-effort: a failed write here must never take down the
// heartbeat loop itself.
func SaveStatus(r TickResult) {
	path := statusPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(StatusSnapshot{TickResult: r, UpdatedAt: time.Now()})
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path) // atomic on the same volume — never leaves a half-written status.json for a concurrent reader
}

// StatusMaxAge is how old a status.json entry can be before LoadStatus
// treats it as stale (the service that was writing it has stopped/isn't
// installed) rather than trusting a long-dead "connected" snapshot.
const StatusMaxAge = 10 * time.Second

// LoadStatus reads the last heartbeat snapshot written by SaveStatus.
// fresh is false when the file is missing, unreadable, or older than
// StatusMaxAge — callers (the tray) should show "serviço não está
// rodando" rather than a stale "conectado" in that case.
func LoadStatus() (snap StatusSnapshot, fresh bool) {
	data, err := os.ReadFile(statusPath())
	if err != nil {
		return StatusSnapshot{}, false
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		return StatusSnapshot{}, false
	}
	return snap, time.Since(snap.UpdatedAt) < StatusMaxAge
}
