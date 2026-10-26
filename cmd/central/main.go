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

	"github.com/cleber/gobackup/internal/api"
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
	case "serve":
		cmdServe(os.Args[2:])
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
	addr := fs.String("addr", ":8080", "listen address")
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
	log.Printf("gobackup-central listening on %s", *addr)
	if err := http.ListenAndServe(*addr, srv.Mux); err != nil {
		fatal(err.Error())
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "error:", msg)
	os.Exit(1)
}
