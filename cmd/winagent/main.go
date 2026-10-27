// Command gobackup-winagent is the Windows-native counterpart to
// gobackup-agent, built for machines where running Docker/Postgres/a web
// UI is undesirable or unavailable (e.g. Windows Server Core). This is the
// headless CLI variant, meant for unattended servers (run `serve` under a
// Scheduled Task). For a desktop machine with a logged-in user, see
// cmd/winagent-tray instead — same engine (internal/winagentcore), with a
// system-tray icon and a local config page instead of a terminal.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cleber/gobackup/internal/poller"
	"github.com/cleber/gobackup/internal/winagentcore"
)

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
	case "create-db-job":
		cmdLocalCreateDBJob(os.Args[2:])
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
	fmt.Fprintln(os.Stderr, `gobackup-winagent — GoBackup agent for Windows

Usage:
  gobackup-winagent register -central <url> [-ca <path-to-ca.pem>] [-name <nome>]
      Registers this machine with a Central and saves the connection locally.
      -name defaults to this machine's hostname if omitted.

  gobackup-winagent serve
      Runs the heartbeat loop in the foreground (1s interval, same as the
      Linux agent) using the saved registration. Ctrl+C to stop. Run this
      under a Scheduled Task to keep it running unattended on a server.
      For a desktop machine, use gobackup-winagent-tray instead — it shows
      a tray icon and needs no terminal window.

  gobackup-winagent status
      Shows the saved registration and does one test heartbeat.

  gobackup-winagent unregister
      Clears the local registration (purely local — does not remove this
      agent from the Central's dashboard; use "Apagar" there for that).

  gobackup-winagent create-job -name <nome> -source <dir> -dest <dir> [-delete-extraneous] [-dest-type local|ssh] [-ssh-host H -ssh-port P -ssh-user U -ssh-key K]
      Creates a file-mirror job locally (no Central needed), for quick
      testing on this machine. Destination can be local/NAS (a plain path)
      or SSH (pure-Go SFTP upload, no external ssh.exe/rsync needed).

  gobackup-winagent create-db-job -name <nome> -engine postgresql|mysql|mariadb|sqlite -dest <dir> [DB connection flags] [SSH dest flags] [-retention-days N]
      Creates a database-dump job locally. Requires pg_dump/pg_dumpall
      (PostgreSQL) or mysqldump (MySQL/MariaDB) to be installed and on
      PATH; SQLite needs nothing extra (plain file copy).

  gobackup-winagent list-jobs
      Lists locally-known jobs (both kinds) and their last run status.

  gobackup-winagent run-job -name <nome> [-password <db password>]
      Runs one locally-known job immediately, printing live progress.
      -password is only used for database jobs and is never saved.

  gobackup-winagent delete-job -name <nome>
      Deletes a locally-known job definition.`)
}

func cmdRegister(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	central := fs.String("central", "", "Central URL, e.g. https://central.example.com:61443")
	caPath := fs.String("ca", "", "Path to the Central's CA certificate PEM (omit if it has a public CA-signed cert)")
	name := fs.String("name", "", "Display name for this agent (defaults to the hostname)")
	fs.Parse(args)

	if *central == "" {
		fmt.Fprintln(os.Stderr, "error: -central is required")
		os.Exit(2)
	}
	cfg, err := winagentcore.RegisterAndSave(*central, *caPath, *name)
	if err != nil {
		fatal(fmt.Sprintf("registration failed: %v", err))
	}
	fmt.Printf("Registrado com sucesso.\n  Central:   %s\n  Agent ID:  %s\n  Nome:      %s\n  Hostname:  %s\n  Config:    %s\n", cfg.CentralURL, cfg.AgentID, cfg.Name, cfg.Hostname, winagentcore.ConfigPath())
	fmt.Println(`Rode "gobackup-winagent serve" para começar a reportar status ao Central.`)
}

func cmdUnregister() {
	cfg, err := winagentcore.LoadConfig()
	if err != nil {
		fatal(err.Error())
	}
	if !cfg.IsConfigured() {
		fmt.Println("Este agente já não está registrado em nenhum Central.")
		return
	}
	if err := winagentcore.Unregister(); err != nil {
		fatal(fmt.Sprintf("clear config: %v", err))
	}
	fmt.Println("Desregistrado deste Central (local). O registro no Central não foi removido —")
	fmt.Println("use o botão \"Apagar\" lá se quiser que ele também suma do dashboard.")
}

func cmdStatus() {
	cfg, err := winagentcore.LoadConfig()
	if err != nil {
		fatal(err.Error())
	}
	if !cfg.IsConfigured() {
		fmt.Println("Não configurado. Rode \"gobackup-winagent register -central <url>\" primeiro.")
		return
	}
	fmt.Printf("Central:   %s\nAgent ID:  %s\nNome:      %s\nHostname:  %s\nConfig:    %s\n\n", cfg.CentralURL, cfg.AgentID, cfg.Name, cfg.Hostname, winagentcore.ConfigPath())

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
	winagentcore.RunLoop(*interval, func(r winagentcore.TickResult) {
		ts := time.Now().Format(time.RFC3339)
		switch {
		case !r.Configured:
			fmt.Printf("[%s] não registrado — rode \"gobackup-winagent register -central <url>\"\n", ts)
		case r.Err != nil:
			fmt.Printf("[%s] heartbeat falhou: %v\n", ts, r.Err)
		case r.CommandsCount > 0:
			fmt.Printf("[%s] heartbeat OK — executou %d comando(s)\n", ts, r.CommandsCount)
		default:
			fmt.Printf("[%s] heartbeat OK\n", ts)
		}
	}, nil)
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
	destType := fs.String("dest-type", "local", "local|nas|ssh")
	sshHost := fs.String("ssh-host", "", "SSH destination host")
	sshPort := fs.Int("ssh-port", 22, "SSH destination port")
	sshUser := fs.String("ssh-user", "", "SSH destination user")
	sshKey := fs.String("ssh-key", "", "path to SSH private key")
	fs.Parse(args)
	if *name == "" || *source == "" || *dest == "" {
		fatal("-name, -source e -dest são obrigatórios")
	}
	store := winagentcore.NewJobStore()
	j, err := store.Create(winagentcore.Job{
		Kind: "files", Name: *name, Source: winagentcore.NormalizeWindowsPath(*source), Dest: winagentcore.NormalizeDestPath(*dest, *destType), DestType: *destType, DeleteExtraneous: *deleteExtraneous,
		SSHHost: *sshHost, SSHPort: *sshPort, SSHUser: *sshUser, SSHKey: *sshKey,
	})
	if err != nil {
		fatal(err.Error())
	}
	fmt.Printf("Tarefa de arquivos criada: id=%s nome=%s\n", j.ID, j.Name)
}

func cmdLocalCreateDBJob(args []string) {
	fs := flag.NewFlagSet("create-db-job", flag.ExitOnError)
	name := fs.String("name", "", "job name")
	engine := fs.String("engine", "", "postgresql|mysql|mariadb|sqlite")
	host := fs.String("host", "", "DB host (empty for sqlite)")
	port := fs.Int("port", 0, "DB port (0 = engine default)")
	username := fs.String("username", "", "DB username")
	database := fs.String("database", "", "database name, or empty for all")
	sqlitePath := fs.String("sqlite-path", "", "path to the .sqlite/.db file (sqlite only)")
	compression := fs.String("compression", "gzip", "gzip|zip|none")
	dest := fs.String("dest", "", "destination directory for the dump")
	destType := fs.String("dest-type", "local", "local|nas|ssh")
	sshHost := fs.String("ssh-host", "", "SSH destination host")
	sshPort := fs.Int("ssh-port", 22, "SSH destination port")
	sshUser := fs.String("ssh-user", "", "SSH destination user")
	sshKey := fs.String("ssh-key", "", "path to SSH private key")
	retention := fs.Int("retention-days", 0, "delete this job's own dumps older than N days (0 = keep forever)")
	fs.Parse(args)
	if *name == "" || *engine == "" || *dest == "" {
		fatal("-name, -engine e -dest são obrigatórios")
	}
	store := winagentcore.NewJobStore()
	j, err := store.Create(winagentcore.Job{
		Kind: "database", Name: *name, Engine: *engine, Host: *host, Port: *port, Username: *username,
		Database: *database, SQLitePath: winagentcore.NormalizeWindowsPath(*sqlitePath), Compression: *compression,
		Dest: winagentcore.NormalizeDestPath(*dest, *destType), DestType: *destType, SSHHost: *sshHost, SSHPort: *sshPort, SSHUser: *sshUser, SSHKey: *sshKey,
		RetentionDays: *retention,
	})
	if err != nil {
		fatal(err.Error())
	}
	fmt.Printf("Tarefa de banco de dados criada: id=%s nome=%s\n", j.ID, j.Name)
}

func cmdLocalListJobs() {
	store := winagentcore.NewJobStore()
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
		if j.Kind == "database" {
			fmt.Printf("%s  [banco]    %-20s %s -> %s  [%s]\n", j.ID, j.Name, j.Engine, j.Dest, status)
		} else {
			fmt.Printf("%s  [arquivos] %-20s %s -> %s  [%s]\n", j.ID, j.Name, j.Source, j.Dest, status)
		}
	}
}

func cmdLocalRunJob(args []string) {
	fs := flag.NewFlagSet("run-job", flag.ExitOnError)
	name := fs.String("name", "", "job name")
	password := fs.String("password", "", "DB password (database jobs only — never saved)")
	fs.Parse(args)
	store := winagentcore.NewJobStore()
	target, found, err := store.GetByName(*name)
	if err != nil {
		fatal(err.Error())
	}
	if !found {
		fatal(fmt.Sprintf("tarefa não encontrada: %s", *name))
	}

	if target.Kind == "database" {
		fmt.Printf("Executando backup de banco %q (%s) -> %s ...\n", target.Name, target.Engine, target.Dest)
		result, err := winagentcore.RunDBJob(context.Background(), target, *password)
		if err != nil {
			_ = store.UpdateStatus(target.ID, "failed", err.Error(), 0, 0)
			fatal(err.Error())
		}
		_ = store.UpdateStatus(target.ID, "success", "", result.BytesWritten, 1)
		fmt.Printf("OK: dump em %s (%d byte(s))", result.OutputPath, result.BytesWritten)
		if result.Deleted > 0 {
			fmt.Printf(", %d backup(s) antigo(s) removido(s) pela retenção", result.Deleted)
		}
		fmt.Println()
		return
	}

	fmt.Printf("Executando %q: %s -> %s ...\n", target.Name, target.Source, target.Dest)
	progress := func(done, total int, bytes int64, file string) {
		fmt.Printf("\r%d/%d arquivos, %d bytes — %s          ", done, total, bytes, file)
	}
	var result winagentcore.MirrorResult
	if target.DestType == "ssh" {
		result, err = winagentcore.MirrorSSH(context.Background(), target.Source, target.SSHHost, target.SSHPort, target.SSHUser, target.SSHKey, target.Dest, target.DeleteExtraneous, progress, nil)
	} else {
		result, err = winagentcore.Mirror(context.Background(), target.Source, target.Dest, target.DeleteExtraneous, progress, nil)
	}
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
	store := winagentcore.NewJobStore()
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
