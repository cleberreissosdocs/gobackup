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
	"net/http"
	"os"
	"time"

	"github.com/cleber/gobackup/internal/agentweb"
	"github.com/cleber/gobackup/internal/backup"
	"github.com/cleber/gobackup/internal/core"
	"github.com/cleber/gobackup/internal/db"
	"github.com/cleber/gobackup/internal/dbjob"
	"github.com/cleber/gobackup/internal/jobexec"
	"github.com/cleber/gobackup/internal/poller"
	"github.com/cleber/gobackup/internal/progress"
	"github.com/cleber/gobackup/internal/scheduler"
	"github.com/cleber/gobackup/internal/tlsutil"
	"github.com/cleber/gobackup/internal/webauth"
	"github.com/cleber/gobackup/internal/webui"
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
	case "run-db":
		cmdRunDB(os.Args[2:])
	case "serve":
		cmdServe(os.Args[2:])
	case "create-user":
		cmdCreateUser(os.Args[2:])
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

	jobID, err := store.CreateFileJob(ctx, tenantID, agentID, *name, *source, *dest, bt,
		*destType, *linkDest, *sshHost, *sshPort, *sshUser, *sshKey, *cron)
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
	res, runErr := backup.Run(ctx, *source, destination, bt, *linkDest, nil)
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
	central := fs.String("central", "", "Central base URL, e.g. https://host:8443")
	name := fs.String("name", "", "agent display name")
	tokenFile := fs.String("token-file", "/etc/gobackup/agent-token", "where to save the issued token")
	centralCA := fs.String("central-ca", "", "path to the Central's TLS CA cert (pinned trust)")
	fs.Parse(args)

	if *central == "" || *name == "" {
		fatal("missing required flags: -central -name")
	}

	hostname, _ := os.Hostname()
	caPEM, err := poller.LoadCACertFile(*centralCA)
	if err != nil {
		fatal(err.Error())
	}
	res, err := poller.Register(*central, *name, hostname, caPEM)
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
	central := fs.String("central", "", "Central base URL, e.g. https://host:8443")
	connString := fs.String("db", "", "agent's local Postgres connection string")
	tokenFile := fs.String("token-file", "/etc/gobackup/agent-token", "file containing the agent token")
	interval := fs.Duration("interval", 1*time.Second, "polling interval")
	once := fs.Bool("once", false, "send a single heartbeat and exit (for testing)")
	centralCA := fs.String("central-ca", "", "path to the Central's TLS CA cert (pinned trust)")
	fs.Parse(args)

	if *central == "" || *connString == "" {
		fatal("missing required flags: -central -db")
	}

	tokenBytes, err := os.ReadFile(*tokenFile)
	if err != nil {
		fatal(fmt.Sprintf("read token file: %v", err))
	}
	caPEM, err := poller.LoadCACertFile(*centralCA)
	if err != nil {
		fatal(err.Error())
	}
	client, err := poller.NewClient(*central, string(tokenBytes), caPEM)
	if err != nil {
		fatal(err.Error())
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
	progressRegistry := progress.NewRegistry()
	exec := &poller.Executor{Store: store, TenantID: tenantID, AgentID: agentID, Engine: &jobexec.Engine{Store: store, Registry: progressRegistry}, Registry: progressRegistry}

	var pendingResults []poller.CommandResult

	beat := func() {
		jobs, _ := store.ListJobs(ctx, agentID)
		jobInfos := make([]poller.JobInfo, 0, len(jobs))
		for _, j := range jobs {
			jobInfos = append(jobInfos, poller.JobInfo{ID: j.ID.String(), Name: j.Name, Kind: j.Kind, Cron: j.Cron, Enabled: j.Enabled})
		}
		running := make([]poller.RunningInfo, 0)
		for jobID, s := range progressRegistry.Snapshot() {
			running = append(running, poller.RunningInfo{JobID: jobID, Status: s.Status, Percent: s.Percent, Message: s.Message})
		}

		resp, err := client.Poll("idle", pendingResults, jobInfos, running)
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

func cmdRunDB(args []string) {
	fs := flag.NewFlagSet("run-db", flag.ExitOnError)
	connString := fs.String("db", "", "agent's local Postgres connection string")
	name := fs.String("name", "", "job name")
	engine := fs.String("engine", "", "mariadb|mysql|postgresql|sqlite")
	host := fs.String("host", "", "database host (not used for sqlite)")
	port := fs.Int("port", 0, "database port (not used for sqlite)")
	user := fs.String("user", "", "database username (not used for sqlite)")
	password := fs.String("password", "", "database password (not used for sqlite); prefer GOBACKUP_DB_PASSWORD env var")
	database := fs.String("database", "", "database name, or __ALL__ for every database (not used for sqlite)")
	sqlitePath := fs.String("sqlite-path", "", "path to the .sqlite/.db file (engine=sqlite only)")
	compression := fs.String("compression", "gzip", "gzip|zip|none")
	dest := fs.String("dest", "", "directory to write the dump into")
	destType := fs.String("dest-type", "local", "local|nas|ssh")
	sshHost := fs.String("ssh-host", "", "ssh destination host (dest-type=ssh)")
	sshPort := fs.Int("ssh-port", 22, "ssh destination port (dest-type=ssh)")
	sshUser := fs.String("ssh-user", "", "ssh destination user (dest-type=ssh)")
	sshKey := fs.String("ssh-key", "", "ssh private key path (dest-type=ssh)")
	fs.Parse(args)

	if *connString == "" || *name == "" || *engine == "" || *dest == "" {
		fatal("missing required flags: -db -name -engine -dest")
	}
	eng := core.DBEngine(*engine)
	switch eng {
	case core.EngineMariaDB, core.EngineMySQL, core.EnginePostgreSQL, core.EngineSQLite:
	default:
		fatal("invalid -engine: " + *engine)
	}
	if eng == core.EngineSQLite && *sqlitePath == "" {
		fatal("engine=sqlite requires -sqlite-path")
	}

	pw := *password
	if pw == "" {
		pw = os.Getenv("GOBACKUP_DB_PASSWORD")
	}

	dbName := *database
	if dbName == "" && eng != core.EngineSQLite {
		dbName = core.AllDatabasesSentinel
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

	connID, err := store.CreateDBConnection(ctx, agentID, *name+"-connection", eng, *host, *port, *user, "", dbName, *sqlitePath)
	if err != nil {
		fatal(err.Error())
	}
	jobID, err := store.CreateDatabaseJob(ctx, tenantID, agentID, connID, *name, core.Compression(*compression),
		*dest, *destType, *sshHost, *sshPort, *sshUser, *sshKey, "")
	if err != nil {
		fatal(err.Error())
	}
	execID, err := store.StartExecution(ctx, jobID, core.TriggerManual)
	if err != nil {
		fatal(err.Error())
	}

	fmt.Printf("[execution %s] dumping %s database %q -> %s (%s)\n", execID, eng, dbName, *dest, *destType)
	start := time.Now()
	res, runErr := dbjob.Run(ctx, dbjob.Request{
		Engine:      eng,
		Host:        *host,
		Port:        *port,
		Username:    *user,
		Password:    pw,
		Database:    dbName,
		SQLitePath:  *sqlitePath,
		Compression: core.Compression(*compression),
		DestType:    *destType,
		DestPath:    *dest,
		SSHHost:     *sshHost,
		SSHPort:     *sshPort,
		SSHUser:     *sshUser,
		SSHKey:      *sshKey,
	})
	elapsed := time.Since(start)

	if runErr != nil {
		_ = store.FinishExecution(ctx, execID, core.ExecFailed, 0, 0, runErr.Error())
		fatal(fmt.Sprintf("db dump failed after %s: %v", elapsed, runErr))
	}

	if err := store.FinishExecution(ctx, execID, core.ExecSuccess, res.BytesWritten, 1, ""); err != nil {
		fatal(err.Error())
	}

	fmt.Printf("[execution %s] OK in %s — wrote %s (%d bytes)\n", execID, elapsed, res.DestPath, res.BytesWritten)
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	connString := fs.String("db", "", "agent's local Postgres connection string")
	addr := fs.String("addr", ":8081", "listen address for the local web UI")
	tlsCert := fs.String("tls-cert", "", "TLS certificate path (auto-generated self-signed if omitted)")
	tlsKey := fs.String("tls-key", "", "TLS key path (auto-generated self-signed if omitted)")
	noTLS := fs.Bool("no-tls", false, "serve plain HTTP instead of HTTPS (NOT recommended)")
	heartbeatInterval := fs.Duration("heartbeat-interval", 1*time.Second, "interval between heartbeats to the configured Central")
	fs.Parse(args)

	if *connString == "" {
		fatal("missing -db")
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

	// Heartbeat runs inside this same process now: its settings (Central
	// URL/CA/token) live in the local DB and can be changed any time from
	// the /settings page, taking effect on the very next tick.
	progressRegistry := progress.NewRegistry()
	engine := &jobexec.Engine{Store: store, Registry: progressRegistry}
	heart := &poller.Runner{Store: store, TenantID: tenantID, AgentID: agentID, Engine: engine, Registry: progressRegistry}
	go heart.Run(ctx, *heartbeatInterval)

	// Background scheduler: fires jobs whose schedule_cron is due, checked
	// every 20s (fine-grained enough vs. the minute-level cron precision).
	go scheduler.Run(ctx, store, engine, agentID, 20*time.Second)

	runner := &webui.LocalRunner{Store: store, TenantID: tenantID, AgentID: agentID, Engine: engine, Registry: progressRegistry}
	auth := webauth.NewManager(store)
	site := &webui.Site{
		Auth: auth,
		GetRunner: func(r *http.Request) (webui.Runner, string, error) {
			return runner, hostname, nil
		},
	}

	mux := http.NewServeMux()
	site.Mount(mux, "")
	(&agentweb.SettingsHandler{Auth: auth, Heart: heart}).Mount(mux)

	if *noTLS {
		log.Printf("gobackup-agent web UI listening on %s (PLAIN HTTP — not recommended)", *addr)
		if err := http.ListenAndServe(*addr, mux); err != nil {
			fatal(err.Error())
		}
		return
	}

	cert, key := *tlsCert, *tlsKey
	if cert == "" || key == "" {
		cert, key = "gobackup-agent-cert.pem", "gobackup-agent-key.pem"
		if err := tlsutil.EnsureCert(cert, key, []string{hostname, "localhost", "127.0.0.1"}); err != nil {
			fatal(fmt.Sprintf("generate self-signed cert: %v", err))
		}
	}
	log.Printf("gobackup-agent web UI listening on https://%s", *addr)
	if err := http.ListenAndServeTLS(*addr, cert, key, mux); err != nil {
		fatal(err.Error())
	}
}

func cmdCreateUser(args []string) {
	fs := flag.NewFlagSet("create-user", flag.ExitOnError)
	connString := fs.String("db", "", "agent's local Postgres connection string")
	email := fs.String("email", "", "login email")
	password := fs.String("password", "", "login password")
	fs.Parse(args)

	if *connString == "" || *email == "" || *password == "" {
		fatal("missing required flags: -db -email -password")
	}

	hash, err := webauth.HashPassword(*password)
	if err != nil {
		fatal(err.Error())
	}

	ctx := context.Background()
	store, err := db.Connect(ctx, *connString)
	if err != nil {
		fatal(err.Error())
	}
	defer store.Close()

	hostname, _ := os.Hostname()
	tenantID, _, err := store.EnsureDefaultTenantAndAgent(ctx, hostname)
	if err != nil {
		fatal(err.Error())
	}

	if _, err := store.CreateUser(ctx, tenantID, *email, hash, core.RoleAdmin); err != nil {
		fatal(err.Error())
	}
	fmt.Printf("user %s created/updated\n", *email)
}
