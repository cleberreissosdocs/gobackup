//go:build windows

package backup

import "os/exec"

// setNewProcessGroup/killProcessGroup stubs for Windows builds — this
// package (rsync-based) is only ever pulled into a Windows BUILD
// transitively (cmd/winagent* -> internal/poller -> internal/jobexec ->
// internal/backup), never actually invoked there: the real Windows agent
// has its own native Go copy engine in internal/winagentcore.
func setNewProcessGroup(cmd *exec.Cmd) {}

func killProcessGroup(pid int) error { return nil }
