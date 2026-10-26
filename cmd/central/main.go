// gobackup-central is the monitoring dashboard binary, deployed on a
// separate infrastructure from the protected servers. For this MVP step it
// supports:
//
//	gobackup-central migrate --db <conn-string>
//	gobackup-central serve --db <conn-string> --addr :8080
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cleber/gobackup/internal/api"
	"github.com/cleber/gobackup/internal/core"
	"github.com/cleber/gobackup/internal/db"
	"github.com/cleber/gobackup/internal/tlsutil"
	"github.com/cleber/gobackup/internal/webauth"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "migrate":
		cmdMigrate(os.Args[2:])
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
	fmt.Fprintln(os.Stderr, "usage: gobackup-central <migrate|serve> [flags]")
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

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	connString := fs.String("db", "", "Postgres connection string")
	addr := fs.String("addr", ":8443", "listen address")
	tlsCert := fs.String("tls-cert", "", "TLS certificate path (auto-generated self-signed if omitted)")
	tlsKey := fs.String("tls-key", "", "TLS key path (auto-generated self-signed if omitted)")
	tlsHosts := fs.String("tls-hosts", "localhost,127.0.0.1", "comma-separated hostnames/IPs for the self-signed cert's SAN (must include the IP agents will connect to)")
	noTLS := fs.Bool("no-tls", false, "serve plain HTTP instead of HTTPS (NOT recommended)")
	fs.Parse(args)

	if *connString == "" {
		fatal("missing -db")
	}

	store, err := db.Connect(context.Background(), *connString)
	if err != nil {
		fatal(err.Error())
	}
	defer store.Close()

	srv := api.NewServer(store)
	go runOfflineChecker(context.Background(), store, srv)

	if *noTLS {
		log.Printf("gobackup-central listening on %s (PLAIN HTTP — not recommended)", *addr)
		if err := http.ListenAndServe(*addr, srv.Mux); err != nil {
			fatal(err.Error())
		}
		return
	}

	cert, key := *tlsCert, *tlsKey
	if cert == "" || key == "" {
		cert, key = "gobackup-central-cert.pem", "gobackup-central-key.pem"
		hosts := strings.Split(*tlsHosts, ",")
		if err := tlsutil.EnsureCert(cert, key, hosts); err != nil {
			fatal(fmt.Sprintf("generate self-signed cert: %v", err))
		}
		log.Printf("self-signed cert generated/reused for hosts %v — copy %s to each agent as -central-ca", hosts, cert)
	}
	log.Printf("gobackup-central listening on https://%s", *addr)
	if err := http.ListenAndServeTLS(*addr, cert, key, srv.Mux); err != nil {
		fatal(err.Error())
	}
}

// runOfflineChecker periodically scans for agents that have gone silent
// and fires the "agent_offline" notification once per outage.
func runOfflineChecker(ctx context.Context, store *db.Store, srv *api.Server) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		agents, err := store.ListAgentsNeedingOfflineAlert(ctx)
		if err != nil {
			log.Println("offline checker:", err)
			continue
		}
		for _, a := range agents {
			tenantID, _, err := store.AgentTenantAndName(ctx, a.ID)
			if err != nil {
				log.Println("offline checker: tenant lookup:", err)
				continue
			}
			srv.Notifier.Dispatch(ctx, tenantID, "agent_offline",
				fmt.Sprintf("[GoBackup] Servidor offline — %s", a.Name),
				fmt.Sprintf("O servidor %q (%s) parou de enviar heartbeat.", a.Name, a.Hostname))
			if err := store.MarkAgentOfflineNotified(ctx, a.ID); err != nil {
				log.Println("offline checker: mark notified:", err)
			}
		}
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "error:", msg)
	os.Exit(1)
}

func cmdCreateUser(args []string) {
	fs := flag.NewFlagSet("create-user", flag.ExitOnError)
	connString := fs.String("db", "", "Postgres connection string")
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

	tenantID, err := store.EnsureDefaultTenant(ctx)
	if err != nil {
		fatal(err.Error())
	}

	if _, err := store.CreateUser(ctx, tenantID, *email, hash, core.RoleAdmin); err != nil {
		fatal(err.Error())
	}
	fmt.Printf("user %s created/updated\n", *email)
}
