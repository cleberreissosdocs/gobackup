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
	"os"
	"time"

	"github.com/cleber/gobackup/internal/backup"
	"github.com/cleber/gobackup/internal/core"
	"github.com/cleber/gobackup/internal/db"
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
	res, runErr := backup.Run(ctx, *source, *dest, bt, *linkDest)
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
