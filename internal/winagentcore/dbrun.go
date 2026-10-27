// Package main (dbrun.go): executes a database-dump job. The actual
// engine-specific dump (pg_dump/pg_dumpall/mysqldump, or a raw SQLite file
// copy) is 100% reused from internal/dbdump — that package already just
// shells out to pg_dump.exe/mysqldump.exe via os/exec, which works
// unmodified on Windows as long as the corresponding client tools are
// installed and on PATH (exactly the same requirement the Linux agent's
// Docker image satisfies by installing postgresql-client/mysql-client).
//
// What IS Windows-specific here is the SSH destination push: the Linux
// agent's internal/dbjob.Run shells out to rsync over ssh for that, but
// rsync isn't available on a stock Windows box, so this uses the pure-Go
// SFTP uploader in sshdest.go instead.
package winagentcore

import (
	"context"
	"fmt"
	"os"

	"github.com/cleber/gobackup/internal/core"
	"github.com/cleber/gobackup/internal/dbdump"
	"github.com/cleber/gobackup/internal/dbjob"
)

// DBRunResult mirrors dbjob.Result.
type DBRunResult struct {
	BytesWritten int64
	OutputPath   string
	Deleted      int // old backups removed by retention cleanup
}

// RunDBJob performs one database backup: dump + (local copy | SSH push) +
// retention cleanup, in that order.
func RunDBJob(ctx context.Context, j Job, password string) (DBRunResult, error) {
	opts := dbdump.Options{
		Engine:     core.DBEngine(j.Engine),
		Host:       j.Host,
		Port:       j.Port,
		Username:   j.Username,
		Password:   password,
		Database:   j.Database,
		SQLitePath: j.SQLitePath,
	}
	compression := core.Compression(j.Compression)
	prefix := dbdump.NamePrefix(opts)

	destType := j.DestType
	if destType == "" {
		destType = "local"
	}

	switch destType {
	case "local", "nas":
		res, err := dbdump.Run(ctx, opts, j.Dest, compression)
		if err != nil {
			return DBRunResult{}, err
		}
		deleted, _ := dbjob.CleanupOld(ctx, dbjob.Request{
			Engine: opts.Engine, Database: opts.Database, SQLitePath: opts.SQLitePath,
			DestType: "local", DestPath: j.Dest,
		}, j.RetentionDays)
		return DBRunResult{BytesWritten: res.BytesWritten, OutputPath: res.OutputPath, Deleted: deleted}, nil

	case "ssh":
		tmpDir, err := os.MkdirTemp("", "gobackup-dbdump-*")
		if err != nil {
			return DBRunResult{}, fmt.Errorf("criar diretório temporário: %w", err)
		}
		defer os.RemoveAll(tmpDir)

		res, err := dbdump.Run(ctx, opts, tmpDir, compression)
		if err != nil {
			return DBRunResult{}, err
		}
		n, err := UploadFile(j.SSHHost, j.SSHPort, j.SSHUser, j.SSHKey, res.OutputPath, j.Dest)
		if err != nil {
			return DBRunResult{}, fmt.Errorf("enviar dump via SSH: %w", err)
		}
		deleted, _ := CleanupOldSSH(j.SSHHost, j.SSHPort, j.SSHUser, j.SSHKey, j.Dest, prefix, j.RetentionDays)
		return DBRunResult{BytesWritten: n, OutputPath: j.Dest + "/" + baseNameOf(res.OutputPath), Deleted: deleted}, nil

	default:
		return DBRunResult{}, fmt.Errorf("tipo de destino não suportado: %s", destType)
	}
}

func baseNameOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}
