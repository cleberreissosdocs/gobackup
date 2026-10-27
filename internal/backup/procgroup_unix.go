//go:build !windows

package backup

import (
	"os/exec"
	"syscall"
)

// setNewProcessGroup puts rsync in its own process group so cancelProcessGroup
// can reach the internal sender/receiver children rsync forks even for a
// purely local copy (not just the single PID Go tracks).
func setNewProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}
