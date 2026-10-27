// Command gobackup-winagent is the Windows-native counterpart to
// gobackup-agent, built for machines where running Docker/Postgres/a web
// UI is undesirable or unavailable (e.g. Windows Server Core, or simply
// "don't want another web app running"). It currently covers ONLY the
// "Configurações" equivalent from the Linux agent: pointing at a Central,
// registering, heartbeating (so the agent shows up online with its job
// list — empty for now), and unregistering. Job execution (actual file/DB
// backups) is a separate, later phase — see internal/poller, which this
// binary reuses as-is (pure Go, no cgo, OS-agnostic) for the wire
// protocol, so nothing here reimplements registration/heartbeat logic.
//
// Config is a local JSON file (no Postgres/web UI on this platform):
// %ProgramData%\GoBackup\config.json — holds the Central URL, the issued
// bearer token, and the pinned CA cert PEM (all local-only, like the
// Linux agent's agent_settings table serves the same purpose there).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cleber/gobackup/internal/poller"
)

type config struct {
	CentralURL string `json:"central_url"`
	Token      string `json:"token"`
	AgentID    string `json:"agent_id"`
	Name       string `json:"name"`
	Hostname   string `json:"hostname"`
	CACertPEM  string `json:"ca_cert_pem"`
}

func configPath() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		// Not running on Windows (or ProgramData unset) — fall back to the
		// working directory so `go run`/local testing still works.
		base = "."
	}
	return filepath.Join(base, "GoBackup", "config.json")
}

func loadConfig() (config, error) {
	var c config
	data, err := os.ReadFile(configPath())
	if err != nil {
		if os.IsNotExist(err) {
			return config{}, nil
		}
		return config{}, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return config{}, fmt.Errorf("parse config: %w", err)
	}
	return c, nil
}

func saveConfig(c config) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// 0600-equivalent: Windows ACLs aren't controlled by this bit, but it's
	// harmless and matches what the Linux side would do for a secrets file.
	return os.WriteFile(path, data, 0o600)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "register":
		cmdRegister(os.Args[2:])
	case "serve":
		cmdServe(os.Args[2:])
	case "unregister", "disconnect":
		cmdUnregister()
	case "status":
		cmdStatus()
	case "create-job":
		cmdLocalCreateJob(os.Args[2:])
	case "list-jobs":
		cmdLocalListJobs()
	case "run-job":
		cmdLocalRunJob(os.Args[2:])
	case "delete-job":
		cmdLocalDeleteJob(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `gobackup-winagent — GoBackup agent for Windows (register/connect/disconnect only, for now)

Usage:
  gobackup-winagent register -central <url> [-ca <path-to-ca.pem>] [-name <nome>]
      Registers this machine with a Central and saves the connection locally.
      -name defaults to this machine's hostname if omitted.

  gobackup-winagent serve
      Runs the heartbeat loop in the foreground (1s interval, same as the
      Linux agent) using the saved registration. Ctrl+C to stop. Run this
      under a Scheduled Task (or as a Windows Service, once added) to keep
      it running unattended.

  gobackup-winagent status
      Shows the saved registration and does one test heartbeat.

  gobackup-winagent unregister
      Clears the local registration (purely local — does not remove this
      agent from the Central's dashboard; use "Apagar" there for that).

  gobackup-winagent create-job -name <nome> -source <dir> -dest <dir> [-delete-extraneous]
      Creates a file-mirror job locally (no Central needed), for quick
      testing on this machine.

  gobackup-winagent list-jobs
      Lists locally-known jobs and their last run status.

  gobackup-winagent run-job -name <nome>
      Runs one locally-known job immediately, printing live progress.

  gobackup-winagent delete-job -name <nome>
      Deletes a locally-known job definition.`)
}

func cmdRegister(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	central := fs.String("central", "", "Central URL, e.g. https://central.example.com:8443")
	caPath := fs.String("ca", "", "Path to the Central's CA certificate PEM (omit if it has a public CA-signed cert)")
	name := fs.String("name", "", "Display name for this agent (defaults to the hostname)")
	fs.Parse(args)

	if *central == "" {
		fmt.Fprintln(os.Stderr, "error: -central is required")
		os.Exit(2)
	}
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "windows-agent"
	}
	agentName := *name
	if agentName == "" {
		agentName = hostname
	}

	var caPEM []byte
	if *caPath != "" {
		caPEM, err = poller.LoadCACertFile(*caPath)
		if err != nil {
			fatal(fmt.Sprintf("read CA cert: %v", err))
		}
	}

	res, err := poller.Register(*central, agentName, hostname, caPEM)
	if err != nil {
		fatal(fmt.Sprintf("registration failed: %v", err))
	}

	cfg := config{
		CentralURL: *central,
		Token:      res.Token,
		AgentID:    res.AgentID,
		Name:       agentName,
		Hostname:   hostname,
		CACertPEM:  string(caPEM),
	}
	if err := saveConfig(cfg); err != nil {
		fatal(fmt.Sprintf("save config: %v", err))
	}
	fmt.Printf("Registrado com sucesso.\n  Central:   %s\n  Agent ID:  %s\n  Nome:      %s\n  Hostname:  %s\n  Config:    %s\n", *central, res.AgentID, agentName, hostname, configPath())
	fmt.Println(`Rode "gobackup-winagent serve" para começar a reportar status ao Central.`)
}

func cmdUnregister() {
	cfg, err := loadConfig()
	if err != nil {
		fatal(err.Error())
	}
	if cfg.CentralURL == "" && cfg.Token == "" {
		fmt.Println("Este agente já não está registrado em nenhum Central.")
		return
	}
	if err := saveConfig(config{}); err != nil {
		fatal(fmt.Sprintf("clear config: %v", err))
	}
	fmt.Println("Desregistrado deste Central (local). O registro no Central não foi removido —")
	fmt.Println("use o botão \"Apagar\" lá se quiser que ele também suma do dashboard.")
}

func cmdStatus() {
	cfg, err := loadConfig()
	if err != nil {
		fatal(err.Error())
	}
	if cfg.CentralURL == "" || cfg.Token == "" {
		fmt.Println("Não configurado. Rode \"gobackup-winagent register -central <url>\" primeiro.")
		return
	}
	fmt.Printf("Central:   %s\nAgent ID:  %s\nNome:      %s\nHostname:  %s\nConfig:    %s\n\n", cfg.CentralURL, cfg.AgentID, cfg.Name, cfg.Hostname, configPath())

	client, err := poller.NewClient(cfg.CentralURL, cfg.Token, []byte(cfg.CACertPEM))
	if err != nil {
		fatal(fmt.Sprintf("build client: %v", err))
	}
	resp, err := client.Poll("idle", nil, []poller.JobInfo{}, []poller.RunningInfo{})
	if err != nil {
		fmt.Printf("Heartbeat de teste FALHOU: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Heartbeat de teste OK (%d comando(s) pendente(s) ignorado(s) neste teste).\n", len(resp.Commands))
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	interval := fs.Duration("interval", time.Second, "heartbeat interval")
	fs.Parse(args)

	fmt.Printf("gobackup-winagent: iniciando heartbeat a cada %s (Ctrl+C para parar)\n", interval.String())
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	store := NewJobStore()
	registry := newProgressRegistry()
	exec := &Executor{Store: store, Registry: registry}
	var pendingResults []poller.CommandResult

	var lastKey string
	var client *poller.Client

	for {
		cfg, err := loadConfig()
		if err != nil {
			fmt.Printf("[%s] erro lendo config: %v\n", time.Now().Format(time.RFC3339), err)
		} else if cfg.CentralURL == "" || cfg.Token == "" {
			fmt.Printf("[%s] não registrado — rode \"gobackup-winagent register -central <url>\"\n", time.Now().Format(time.RFC3339))
		} else {
			key := cfg.CentralURL + "|" + cfg.Token + "|" + cfg.CACertPEM
			if client == nil || key != lastKey {
				client, err = poller.NewClient(cfg.CentralURL, cfg.Token, []byte(cfg.CACertPEM))
				if err != nil {
					fmt.Printf("[%s] erro criando cliente: %v\n", time.Now().Format(time.RFC3339), err)
					client = nil
				}
				lastKey = key
			}
			if client != nil {
				jobs, _ := store.List()
				jobInfos := make([]poller.JobInfo, 0, len(jobs))
				for _, j := range jobs {
					jobInfos = append(jobInfos, poller.JobInfo{ID: j.ID, Name: j.Name, Kind: "files", Cron: j.Cron, Enabled: j.Enabled, LastStatus: j.LastStatus})
				}
				running := make([]poller.RunningInfo, 0)
				for jobID, s := range registry.snapshot() {
					running = append(running, poller.RunningInfo{JobID: jobID, Status: s.Status, Percent: s.Percent, Message: s.Message})
				}

				resp, err := client.Poll("idle", pendingResults, jobInfos, running)
				pendingResults = nil
				if err != nil {
					fmt.Printf("[%s] heartbeat falhou: %v\n", time.Now().Format(time.RFC3339), err)
				} else {
					if len(resp.Commands) > 0 {
						fmt.Printf("[%s] heartbeat OK — executando %d comando(s)\n", time.Now().Format(time.RFC3339), len(resp.Commands))
					}
					for _, cmd := range resp.Commands {
						fmt.Printf("  comando %s (%s)\n", cmd.ID, cmd.Type)
						pendingResults = append(pendingResults, exec.Execute(cmd))
					}
					if len(resp.Commands) == 0 {
						fmt.Printf("[%s] heartbeat OK\n", time.Now().Format(time.RFC3339))
					}
				}
			}
		}
		<-ticker.C
	}
}

// --- Local CLI commands, usable without any Central at all — handy for
// quick verification on a single machine (create a job, run it, check the
// result) before wiring it up to a real Central.

func cmdLocalCreateJob(args []string) {
	fs := flag.NewFlagSet("create-job", flag.ExitOnError)
	name := fs.String("name", "", "job name")
	source := fs.String("source", "", "source directory")
	dest := fs.String("dest", "", "destination directory")
	deleteExtraneous := fs.Bool("delete-extraneous", false, "mirror deletions too (rsync --delete equivalent)")
	fs.Parse(args)
	if *name == "" || *source == "" || *dest == "" {
		fatal("-name, -source e -dest são obrigatórios")
	}
	store := NewJobStore()
	j, err := store.Create(FileJob{Name: *name, Source: *source, Dest: *dest, DestType: "local", DeleteExtraneous: *deleteExtraneous})
	if err != nil {
		fatal(err.Error())
	}
	fmt.Printf("Tarefa criada: id=%s nome=%s\n", j.ID, j.Name)
}

func cmdLocalListJobs() {
	store := NewJobStore()
	jobs, err := store.List()
	if err != nil {
		fatal(err.Error())
	}
	if len(jobs) == 0 {
		fmt.Println("nenhuma tarefa criada ainda")
		return
	}
	for _, j := range jobs {
		status := j.LastStatus
		if status == "" {
			status = "nunca executada"
		}
		fmt.Printf("%s  %-20s %s -> %s  [%s]\n", j.ID, j.Name, j.Source, j.Dest, status)
	}
}

func cmdLocalRunJob(args []string) {
	fs := flag.NewFlagSet("run-job", flag.ExitOnError)
	name := fs.String("name", "", "job name")
	fs.Parse(args)
	store := NewJobStore()
	jobs, err := store.List()
	if err != nil {
		fatal(err.Error())
	}
	var target *FileJob
	for i := range jobs {
		if jobs[i].Name == *name {
			target = &jobs[i]
			break
		}
	}
	if target == nil {
		fatal(fmt.Sprintf("tarefa não encontrada: %s", *name))
	}
	fmt.Printf("Executando %q: %s -> %s ...\n", target.Name, target.Source, target.Dest)
	result, err := Mirror(target.Source, target.Dest, target.DeleteExtraneous, func(done, total int, bytes int64, file string) {
		fmt.Printf("\r%d/%d arquivos, %d bytes — %s          ", done, total, bytes, file)
	})
	fmt.Println()
	if err != nil {
		_ = store.UpdateStatus(target.ID, "failed", err.Error(), 0, 0)
		fatal(err.Error())
	}
	_ = store.UpdateStatus(target.ID, "success", "", result.BytesCopied, result.FilesCopied)
	fmt.Printf("OK: %d copiado(s), %d pulado(s), %d removido(s), %d byte(s)\n", result.FilesCopied, result.FilesSkipped, result.FilesDeleted, result.BytesCopied)
}

func cmdLocalDeleteJob(args []string) {
	fs := flag.NewFlagSet("delete-job", flag.ExitOnError)
	name := fs.String("name", "", "job name")
	fs.Parse(args)
	store := NewJobStore()
	jobs, err := store.List()
	if err != nil {
		fatal(err.Error())
	}
	for _, j := range jobs {
		if j.Name == *name {
			if err := store.Delete(j.ID); err != nil {
				fatal(err.Error())
			}
			fmt.Println("Tarefa apagada.")
			return
		}
	}
	fatal(fmt.Sprintf("tarefa não encontrada: %s", *name))
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "erro:", msg)
	os.Exit(1)
}
