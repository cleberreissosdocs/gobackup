//go:build windows

// Package main (service_windows.go): registers/runs gobackup-winagent as a
// real Windows Service (Local System, Automatic start) — this is what lets
// the agent keep polling the Central and running scheduled backups with
// NOBODY logged into the machine. The previous setup (tray app autostarted
// via the HKLM Run key) only ever launched at an interactive logon, which
// is useless for an unattended server reboot — exactly the bug this file
// fixes.
package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/cleber/gobackup/internal/core"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/cleber/gobackup/internal/winagentcore"
)

const serviceName = "GoBackupAgent"
const serviceDisplayName = "GoBackup Agent"
const serviceDescription = "Executa backups agendados e mantém este servidor conectado ao Central GoBackup. Roda mesmo sem nenhum usuário com sessão aberta."

// cmdServiceInstall registers this exact executable (with "service-run"
// appended) as a Windows Service set to start automatically at boot, and
// starts it immediately — so `service-install` alone is enough to make the
// agent survive the very next reboot with no further action.
func cmdServiceInstall() {
	exePath, err := os.Executable()
	if err != nil {
		fatal("não foi possível determinar o caminho do executável: " + err.Error())
	}

	m, err := mgr.Connect()
	if err != nil {
		fatal("conectar ao Service Control Manager (rode como Administrador): " + err.Error())
	}
	defer m.Disconnect()

	// An upgrade re-runs this with the service ALREADY installed, usually
	// still pointing at the previous build's path. Merely starting it
	// again (what this used to do) left the machine running the OLD
	// binary while the installer reported success — the operator believes
	// they upgraded and nothing in the UI says otherwise. So: stop it,
	// repoint it at the executable that was just installed, start it.
	if existing, err := m.OpenService(serviceName); err == nil {
		defer existing.Close()
		wanted := `"` + exePath + `" service-run`

		cfg, err := existing.Config()
		if err != nil {
			fatal("ler a configuração do serviço existente: " + err.Error())
		}
		if cfg.BinaryPathName == wanted {
			fmt.Println("Serviço já aponta para esta instalação.")
		} else {
			if err := stopService(existing); err != nil {
				fatal("parar o serviço para atualizá-lo: " + err.Error())
			}
			cfg.BinaryPathName = wanted
			cfg.DisplayName = serviceDisplayName
			cfg.Description = serviceDescription
			cfg.StartType = mgr.StartAutomatic
			if err := existing.UpdateConfig(cfg); err != nil {
				fatal("apontar o serviço para o novo executável: " + err.Error())
			}
			fmt.Println("Serviço atualizado para esta instalação: " + exePath)
		}
		cmdServiceStartOnly(m)
		return
	}

	s, err := m.CreateService(serviceName, exePath, mgr.Config{
		DisplayName:  serviceDisplayName,
		Description:  serviceDescription,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
		// Deliberately NOT DelayedAutoStart: that trades roughly a 2-minute
		// delay for avoiding a race with network availability at boot —
		// unnecessary here, since RunLoop already retries every second and
		// self-heals the moment the network comes up, so an early start
		// that briefly fails its first few ticks reconnects far sooner
		// than waiting out a fixed 2-minute delay ever would.
	}, "service-run")
	if err != nil {
		fatal("criar o serviço: " + err.Error())
	}
	defer s.Close()

	if err := s.Start(); err != nil {
		fmt.Printf("Serviço criado, mas falhou ao iniciar agora: %v (vai iniciar sozinho no próximo boot)\n", err)
		return
	}
	fmt.Println("Serviço \"" + serviceDisplayName + "\" instalado e iniciado — sobrevive a reboots mesmo sem login.")
}

// stopService asks the SCM to stop the service and waits for it to really
// be stopped — the file cannot be replaced, nor the service repointed
// safely, while the process is still holding it. A service already stopped
// is success, not an error.
func stopService(s *mgr.Service) error {
	status, err := s.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Stopped {
		return nil
	}
	if _, err := s.Control(svc.Stop); err != nil && status.State != svc.StopPending {
		return err
	}
	// 30s is generous for this agent: the loop ticks every second and
	// holds no long transaction. Failing loudly beats leaving the caller
	// to overwrite a file still in use.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		status, err := s.Query()
		if err != nil {
			return err
		}
		if status.State == svc.Stopped {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("o serviço não parou em 30s")
}

func cmdServiceStartOnly(m *mgr.Mgr) {
	s, err := m.OpenService(serviceName)
	if err != nil {
		return
	}
	defer s.Close()
	if err := s.Start(); err != nil {
		fmt.Printf("(já estava instalado; tentativa de iniciar: %v)\n", err)
	} else {
		fmt.Println("Serviço iniciado.")
	}
}

// cmdServiceUninstall stops and removes the service. It does NOT touch the
// saved registration/jobs (those live in %ProgramData%\GoBackup, untouched
// by this) — only the SCM entry goes away.
func cmdServiceUninstall() {
	m, err := mgr.Connect()
	if err != nil {
		fatal("conectar ao Service Control Manager (rode como Administrador): " + err.Error())
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		fmt.Println("Serviço não estava instalado.")
		return
	}
	defer s.Close()

	if status, err := s.Query(); err == nil && status.State != svc.Stopped {
		_, _ = s.Control(svc.Stop)
		for i := 0; i < 30; i++ {
			status, err := s.Query()
			if err != nil || status.State == svc.Stopped {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	if err := s.Delete(); err != nil {
		fatal("remover o serviço: " + err.Error())
	}
	fmt.Println("Serviço removido.")
}

// gbServiceHandler implements svc.Handler: starts the SAME heartbeat
// engine `serve` and the tray use (winagentcore.RunLoop) in the
// background, reports status transitions to the SCM, and cleanly stops the
// loop on a Stop/Shutdown control request.
type gbServiceHandler struct{}

func (h *gbServiceHandler) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	// A service has no console, so without this every log line the agent
	// writes -- including why a scheduled backup did not run -- would go
	// nowhere. Not fatal: an agent that backs up but cannot log beats one
	// that refuses to start over a logging problem.
	if err := winagentcore.SetupFileLogging(); err != nil {
		winagentcore.SaveStatus(winagentcore.TickResult{Err: err})
	}
	log.Printf("gobackup: serviço iniciado; log em %s", winagentcore.LogPath())

	stop := make(chan struct{})
	// The service ticks every second and, until now, wrote nothing to the
	// log when the Central was unreachable — the failure only reached the
	// status file the UI reads, so an outage left no trace to look at
	// afterwards. Logging every tick is not an option here either: this
	// log rotates at 2 MiB keeping one generation, so a few hours of
	// spam would evict the history worth keeping. One line when the
	// connection breaks, one when it returns.
	var beatFailure core.RepeatedError
	go winagentcore.RunLoop(time.Second, func(res winagentcore.TickResult) {
		winagentcore.SaveStatus(res)
		switch {
		case res.Err != nil:
			beatFailure.Report("heartbeat", res.Err, log.Printf)
		case res.Connected:
			beatFailure.Resolved("heartbeat", log.Printf)
		}
		// An agent not yet enrolled with a Central is neither connected
		// nor failing: it stays silent, as it should.
	}, stop)

	changes <- svc.Status{State: svc.Running, Accepts: accepted}

	for req := range r {
		switch req.Cmd {
		case svc.Interrogate:
			changes <- req.CurrentStatus
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			close(stop)
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		}
	}
	return false, 0
}

// cmdServiceRun is the entry point registered with the SCM
// ("service-run" is what CreateService above points at). When the SCM
// actually launches it, svc.IsWindowsService() is true and svc.Run blocks
// for the service's whole lifetime talking to the SCM. Running it directly
// from an interactive prompt (svc.IsWindowsService() false — handy for
// testing without installing anything) just falls back to the same
// foreground loop `serve` uses.
func cmdServiceRun(args []string) {
	isService, err := svc.IsWindowsService()
	if err != nil {
		fatal("detectar contexto de serviço: " + err.Error())
	}
	if !isService {
		fmt.Println("(rodando fora do Service Control Manager — usando o mesmo loop em primeiro plano que 'serve'; Ctrl+C para parar)")
		cmdServe(args)
		return
	}
	if err := svc.Run(serviceName, &gbServiceHandler{}); err != nil {
		os.Exit(1)
	}
}
