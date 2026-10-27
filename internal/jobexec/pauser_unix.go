//go:build !windows

package jobexec

import (
	"fmt"
	"os"
	"sync"
	"syscall"
)

// pauser wraps the OS process backing a running file-mirror job, sending
// real SIGSTOP/SIGCONT — its I/O genuinely stops until resumed, not a
// cooperative flag a busy loop has to check.
type pauser struct {
	mu   sync.Mutex
	proc *os.Process
}

func (p *pauser) setProcess(proc *os.Process) {
	p.mu.Lock()
	p.proc = proc
	p.mu.Unlock()
}

func (p *pauser) Pause() error {
	p.mu.Lock()
	proc := p.proc
	p.mu.Unlock()
	if proc == nil {
		return fmt.Errorf("a execução ainda está iniciando, tente novamente em instantes")
	}
	return proc.Signal(syscall.SIGSTOP)
}

func (p *pauser) Resume() error {
	p.mu.Lock()
	proc := p.proc
	p.mu.Unlock()
	if proc == nil {
		return fmt.Errorf("a execução ainda está iniciando, tente novamente em instantes")
	}
	return proc.Signal(syscall.SIGCONT)
}
