// gobackup-agent is the binary installed on each protected Linux server.
// For this MVP step it supports two subcommands:
//
//	gobackup-agent migrate --db <conn-string>
//	    applies the embedded schema migrations to the agent's local Postgres.
//
//	gobackup-agent run --db <conn-string> --name <job-name> --source <dir> --dest <dir> --type full|incremental|differential
//	    runs (and creates, if it doesn't exist yet) a file-tree backup job
//	    once, recording the execution in the local Postgres.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/cleber/gobackup/internal/backup"
	"github.com/cleber/gobackup/internal/core"
	"github.com/cleber/gobackup/internal/db"
	"github.com/cleber/gobackup/internal/poller"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "migrate":
		cmdMigrate(os.Args[2:])
	case "run":
		cmdRun(os.Args[2:])
	case "register":
		cmdRegister(os.Args[2:])
	case "heartbeat":
		cmdHeartbeat(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: gobackup-agent <migrate|run> [flags]")
}

func cmdMigrate(args []string) {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	connString := fs.String("db", "", "Postgres connection string")
	fs.Parse(args)

	if *connString == "" {
		fatal("missing -db")
	}
	if err := db.Migrate(*connString); err != nil {
		fatal(err.Error())
	}
	fmt.Println("migration OK")
}

func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	connString := fs.String("db", "", "Postgres connection string")
	name := fs.String("name", "", "job name")
	source := fs.String("source", "", "source directory")
	dest := fs.String("dest", "", "destination directory")
	backupType := fs.String("type", "full", "full|incremental|differential")
	linkDest := fs.String("link-dest", "", "rsync --link-dest baseline (for incremental/differential)")
	cron := fs.String("cron", "", "schedule cron expression (informational for this MVP step)")
	destType := fs.String("dest-type", "local", "local|nas|ssh")
	sshHost := fs.String("ssh-host", "", "ssh destination host (dest-type=ssh)")
	sshPort := fs.Int("ssh-port", 22, "ssh destination port (dest-type=ssh)")
	sshUser := fs.String("ssh-user", "", "ssh destination user (dest-type=ssh)")
	sshKey := fs.String("ssh-key", "", "ssh private key path (dest-type=ssh)")
	fs.Parse(args)

	if *connString == "" || *name == "" || *source == "" || *dest == "" {
		fatal("missing required flags: -db -name -source -dest")
	}

	bt := core.BackupType(*backupType)
	switch bt {
	case core.BackupFull, core.BackupIncremental, core.BackupDifferential:
	default:
		fatal("invalid -type: " + *backupType)
	}

	ctx := context.Background()

	store, err := db.Connect(ctx, *connString)
	if err != nil {
		fatal(err.Error())
	}
	defer store.Close()

	hostname, _ := os.Hostname()
	tenantID, agentID, err := store.EnsureDefaultTenantAndAgent(ctx, hostname)
	if err != nil {
		fatal(err.Error())
	}

	jobID, err := store.CreateFileJob(ctx, tenantID, agentID, *name, *source, *dest, bt, *cron)
	if err != nil {
		fatal(err.Error())
	}

	execID, err := store.StartExecution(ctx, jobID, core.TriggerManual)
	if err != nil {
		fatal(err.Error())
	}

	fmt.Printf("[execution %s] running rsync (%s): %s -> %s\n", execID, bt, *source, *dest)
	start := time.Now()
	destination, err := buildDestination(*destType, *dest, *sshHost, *sshPort, *sshUser, *sshKey)
	if err != nil {
		fatal(err.Error())
	}
	res, runErr := backup.Run(ctx, *source, destination, bt, *linkDest)
	elapsed := time.Since(start)

	if runErr != nil {
		_ = store.FinishExecution(ctx, execID, core.ExecFailed, 0, 0, runErr.Error())
		fatal(fmt.Sprintf("backup failed after %s: %v", elapsed, runErr))
	}

	if err := store.FinishExecution(ctx, execID, core.ExecSuccess, res.BytesTransferred, res.FilesTransferred, ""); err != nil {
		fatal(err.Error())
	}

	fmt.Printf("[execution %s] OK in %s — files=%d bytes=%d\n", execID, elapsed, res.FilesTransferred, res.BytesTransferred)
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "error:", msg)
	os.Exit(1)
}

func cmdRegister(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	central := fs.String("central", "", "Central base URL, e.g. http://host:8080")
	name := fs.String("name", "", "agent display name")
	tokenFile := fs.String("token-file", "/etc/gobackup/agent-token", "where to save the issued token")
	fs.Parse(args)

	if *central == "" || *name == "" {
		fatal("missing required flags: -central -name")
	}

	hostname, _ := os.Hostname()
	res, err := poller.Register(*central, *name, hostname)
	if err != nil {
		fatal(err.Error())
	}

	if err := os.MkdirAll(dirOf(*tokenFile), 0o700); err != nil {
		fatal(fmt.Sprintf("create token dir: %v", err))
	}
	if err := os.WriteFile(*tokenFile, []byte(res.Token), 0o600); err != nil {
		fatal(fmt.Sprintf("write token file: %v", err))
	}

	fmt.Printf("registered agent_id=%s, token saved to %s\n", res.AgentID, *tokenFile)
}

func cmdHeartbeat(args []string) {
	fs := flag.NewFlagSet("heartbeat", flag.ExitOnError)
	central := fs.String("central", "", "Central base URL, e.g. http://host:8080")
	connString := fs.String("db", "", "agent's local Postgres connection string")
	tokenFile := fs.String("token-file", "/etc/gobackup/agent-token", "file containing the agent token")
	interval := fs.Duration("interval", 15*time.Second, "polling interval")
	once := fs.Bool("once", false, "send a single heartbeat and exit (for testing)")
	fs.Parse(args)

	if *central == "" || *connString == "" {
		fatal("missing required flags: -central -db")
	}

	tokenBytes, err := os.ReadFile(*tokenFile)
	if err != nil {
		fatal(fmt.Sprintf("read token file: %v", err))
	}
	client := poller.NewClient(*central, string(tokenBytes))

	ctx := context.Background()
	store, err := db.Connect(ctx, *connString)
	if err != nil {
		fatal(err.Error())
	}
	defer store.Close()

	hostname, _ := os.Hostname()
	tenantID, agentID, err := store.EnsureDefaultTenantAndAgent(ctx, hostname)
	if err != nil {
		fatal(err.Error())
	}
	exec := &poller.Executor{Store: store, TenantID: tenantID, AgentID: agentID}

	var pendingResults []poller.CommandResult

	beat := func() {
		resp, err := client.Poll("idle", pendingResults)
		if err != nil {
			log.Println("heartbeat error:", err)
			return
		}
		pendingResults = nil
		log.Printf("heartbeat OK, %d command(s) received", len(resp.Commands))

		for _, cmd := range resp.Commands {
			log.Printf("executing command %s (%s)", cmd.ID, cmd.Type)
			result := exec.Execute(ctx, cmd)
			if result.Status == "failed" {
				log.Printf("command %s failed: %v", cmd.ID, result.Result["error"])
			} else {
				log.Printf("command %s OK", cmd.ID)
			}
			pendingResults = append(pendingResults, result)
		}
	}

	beat()
	if *once {
		// give pending results (from commands executed above) one more
		// chance to reach the Central before exiting.
		if len(pendingResults) > 0 {
			beat()
		}
		return
	}
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for range ticker.C {
		beat()
	}
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}

// buildDestination turns the -dest-type/-ssh-* CLI flags into a
// backup.Destination value.
func buildDestination(destType, path, sshHost string, sshPort int, sshUser, sshKey string) (backup.Destination, error) {
	switch destType {
	case "", "local":
		return backup.Local(path), nil
	case "nas":
		return backup.NAS(path), nil
	case "ssh":
		if sshHost == "" || sshUser == "" {
			return backup.Destination{}, fmt.Errorf("dest-type=ssh requires -ssh-host and -ssh-user")
		}
		return backup.SSH(sshHost, sshPort, sshUser, sshKey, path), nil
	default:
		return backup.Destination{}, fmt.Errorf("invalid -dest-type: %s", destType)
	}
}
