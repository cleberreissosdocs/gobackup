// Package dbjob glues together dbdump (the actual engine-specific dump) and
// the backup package's destination transport (local/nas/ssh), so a database
// backup can land on any of the same destinations a file backup can.
package dbjob

import (
	"context"
	"fmt"
	"os"

	"github.com/cleber/gobackup/internal/backup"
	"github.com/cleber/gobackup/internal/core"
	"github.com/cleber/gobackup/internal/dbdump"
)

// Request fully describes one database backup run.
type Request struct {
	Engine     core.DBEngine
	Host       string
	Port       int
	Username   string
	Password   string
	Database   string // or core.AllDatabasesSentinel
	SQLitePath string

	Compression core.Compression

	DestType string // "local" | "nas" | "ssh" (default "local")
	DestPath string
	SSHHost  string
	SSHPort  int
	SSHUser  string
	SSHKey   string
}

// Result mirrors dbdump.Result plus which destination it ended up at.
type Result struct {
	BytesWritten int64
	DestPath     string
}

// Run performs the dump and, for local/nas destinations, writes the
// compressed dump straight into DestPath; for an ssh destination, it dumps
// to a local temp file first and then pushes that single file over rsync+ssh
// (reusing internal/backup, so credentials/flags behave identically to file
// backups).
func Run(ctx context.Context, req Request) (Result, error) {
	opts := dbdump.Options{
		Engine:     req.Engine,
		Host:       req.Host,
		Port:       req.Port,
		Username:   req.Username,
		Password:   req.Password,
		Database:   req.Database,
		SQLitePath: req.SQLitePath,
	}

	destType := req.DestType
	if destType == "" {
		destType = "local"
	}

	switch destType {
	case "local", "nas":
		res, err := dbdump.Run(ctx, opts, req.DestPath, req.Compression)
		if err != nil {
			return Result{}, err
		}
		return Result{BytesWritten: res.BytesWritten, DestPath: res.OutputPath}, nil

	case "ssh":
		tmpDir, err := os.MkdirTemp("", "gobackup-dbdump-*")
		if err != nil {
			return Result{}, fmt.Errorf("create temp dir: %w", err)
		}
		defer os.RemoveAll(tmpDir)

		dumpRes, err := dbdump.Run(ctx, opts, tmpDir, req.Compression)
		if err != nil {
			return Result{}, err
		}

		destination := backup.SSH(req.SSHHost, req.SSHPort, req.SSHUser, req.SSHKey, req.DestPath)
		transferRes, err := backup.Run(ctx, tmpDir, destination, core.BackupFull, "")
		if err != nil {
			return Result{}, fmt.Errorf("push dump over ssh: %w", err)
		}
		return Result{BytesWritten: transferRes.BytesTransferred, DestPath: req.DestPath + "/" + baseName(dumpRes.OutputPath)}, nil

	default:
		return Result{}, fmt.Errorf("unsupported destination type: %s", destType)
	}
}

func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
