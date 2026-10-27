//go:build windows

package jobexec

import (
	"fmt"
	"os"
	"sync"
)

// pauser stub for Windows builds — this package is only ever pulled into
// a Windows build TRANSITIVELY (cmd/winagent* imports internal/poller,
// which shares wire-protocol types with internal/jobexec's Engine field)
// never actually instantiated/run there: the real Windows agent uses its
// own internal/winagentcore.Executor + cooperative PauseGate instead.
// SIGSTOP/SIGCONT don't exist on Windows, so these just report "not
// supported" if somehow ever reached.
type pauser struct {
	mu  sync.Mutex
	set bool
}

func (p *pauser) setProcess(_ *os.Process) {
	p.mu.Lock()
	p.set = true
	p.mu.Unlock()
}

func (p *pauser) Pause() error {
	return fmt.Errorf("pausar não é suportado nesta plataforma")
}

func (p *pauser) Resume() error {
	return fmt.Errorf("retomar não é suportado nesta plataforma")
}
