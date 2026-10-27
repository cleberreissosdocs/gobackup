// Package dbdump orchestrates database backups (dump + compression) for
// the four engines GoBackup supports: MariaDB, MySQL, PostgreSQL and
// SQLite. The dump is written compressed directly to the destination
// directory configured by the user — no uncompressed intermediate file.
package dbdump

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cleber/gobackup/internal/core"
)

// Options describes the source database connection to dump.
type Options struct {
	Engine   core.DBEngine
	Host     string
	Port     int
	Username string
	Password string // never logged; passed via env var to the dump tool, not argv

	// Database is the schema/database name, or core.AllDatabasesSentinel to
	// dump every database the engine knows about. Ignored for SQLite.
	Database string

	// SQLitePath is the path to the .db/.sqlite file. Only used when
	// Engine == core.EngineSQLite.
	SQLitePath string
}

// Result summarizes a finished dump.
type Result struct {
	OutputPath   string
	BytesWritten int64
}

// Run performs the dump and writes the (optionally compressed) result into
// destDir, returning the path written.
func Run(ctx context.Context, opts Options, destDir string, compression core.Compression) (Result, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create destination dir: %w", err)
	}

	baseName := dumpBaseName(opts)
	ext := ".sql"
	if opts.Engine == core.EngineSQLite {
		ext = ".sqlite"
	}

	var outPath string
	switch compression {
	case core.CompressionGzip:
		outPath = filepath.Join(destDir, baseName+ext+".gz")
	case core.CompressionZip:
		outPath = filepath.Join(destDir, baseName+ext+".zip")
	default:
		outPath = filepath.Join(destDir, baseName+ext)
	}

	out, err := os.Create(outPath)
	if err != nil {
		return Result{}, fmt.Errorf("create output file: %w", err)
	}
	defer out.Close()

	var writer io.Writer = out
	var closers []io.Closer

	switch compression {
	case core.CompressionGzip:
		gw := gzip.NewWriter(out)
		writer = gw
		closers = append(closers, gw)
	case core.CompressionZip:
		zw := newSingleFileZipWriter(out, baseName+ext)
		writer = zw
		closers = append(closers, zw)
	}

	var written int64
	switch opts.Engine {
	case core.EngineSQLite:
		written, err = dumpSQLite(ctx, opts, writer)
	case core.EnginePostgreSQL:
		written, err = dumpPostgres(ctx, opts, writer)
	case core.EngineMySQL, core.EngineMariaDB:
		written, err = dumpMySQLFamily(ctx, opts, writer)
	default:
		err = fmt.Errorf("unsupported engine: %s", opts.Engine)
	}

	for i := len(closers) - 1; i >= 0; i-- {
		if cerr := closers[i].Close(); cerr == nil && err == nil {
			// ok
		} else if cerr != nil && err == nil {
			err = cerr
		}
	}
	if err != nil {
		os.Remove(outPath)
		return Result{}, err
	}

	return Result{OutputPath: outPath, BytesWritten: written}, nil
}

func dumpBaseName(opts Options) string {
	return namePrefix(opts) + time.Now().UTC().Format("20060102-150405")
}

// namePrefix returns the stable (non-timestamp) part of a dump's filename
// — e.g. "postgresql-mydb-" — so retention cleanup can find exactly this
// job's own past backups among whatever else may live in the destination
// directory, without touching unrelated files.
func namePrefix(opts Options) string {
	name := opts.Database
	if opts.Engine == core.EngineSQLite {
		name = filepath.Base(opts.SQLitePath)
	} else if name == core.AllDatabasesSentinel || name == "" {
		name = "all-databases"
	}
	return fmt.Sprintf("%s-%s-", opts.Engine, name)
}

// NamePrefix is the exported form of namePrefix, used by the dbjob package
// to scope retention cleanup to this job's own backup files.
func NamePrefix(opts Options) string {
	return namePrefix(opts)
}

func dumpSQLite(ctx context.Context, opts Options, w io.Writer) (int64, error) {
	if opts.SQLitePath == "" {
		return 0, fmt.Errorf("sqlite_path is required")
	}
	f, err := os.Open(opts.SQLitePath)
	if err != nil {
		return 0, fmt.Errorf("open sqlite file: %w", err)
	}
	defer f.Close()
	// Plain io.Copy would ignore Cancel entirely for a big SQLite file —
	// copy in chunks and check ctx between them so a cancelled dump
	// actually stops instead of running to completion regardless.
	buf := make([]byte, 4*1024*1024)
	var total int64
	for {
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
		n, rerr := f.Read(buf)
		if n > 0 {
			wn, werr := w.Write(buf[:n])
			total += int64(wn)
			if werr != nil {
				return total, werr
			}
		}
		if rerr == io.EOF {
			return total, nil
		}
		if rerr != nil {
			return total, rerr
		}
	}
}

func dumpPostgres(ctx context.Context, opts Options, w io.Writer) (int64, error) {
	var args []string
	tool := "pg_dump"
	if opts.Database == core.AllDatabasesSentinel || opts.Database == "" {
		tool = "pg_dumpall"
	}
	if opts.Host != "" {
		args = append(args, "-h", opts.Host)
	}
	if opts.Port != 0 {
		args = append(args, "-p", fmt.Sprintf("%d", opts.Port))
	}
	if opts.Username != "" {
		args = append(args, "-U", opts.Username)
	}
	if tool == "pg_dump" {
		args = append(args, opts.Database)
	}

	cmd := exec.CommandContext(ctx, tool, args...)
	if opts.Password != "" {
		cmd.Env = append(os.Environ(), "PGPASSWORD="+opts.Password)
	}
	return runPiped(cmd, w)
}

func dumpMySQLFamily(ctx context.Context, opts Options, w io.Writer) (int64, error) {
	tool := "mysqldump" // works for both MySQL and MariaDB client libraries
	var args []string
	if opts.Host != "" {
		args = append(args, "-h", opts.Host)
	}
	if opts.Port != 0 {
		args = append(args, "-P", fmt.Sprintf("%d", opts.Port))
	}
	if opts.Username != "" {
		args = append(args, "-u", opts.Username)
	}
	if opts.Database == core.AllDatabasesSentinel || opts.Database == "" {
		args = append(args, "--all-databases")
	} else {
		args = append(args, opts.Database)
	}

	cmd := exec.CommandContext(ctx, tool, args...)
	if opts.Password != "" {
		cmd.Env = append(os.Environ(), "MYSQL_PWD="+opts.Password)
	}
	return runPiped(cmd, w)
}

func runPiped(cmd *exec.Cmd, w io.Writer) (int64, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("stdout pipe: %w", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start %s: %w", cmd.Path, err)
	}

	n, copyErr := io.Copy(w, stdout)
	waitErr := cmd.Wait()
	if waitErr != nil {
		return n, fmt.Errorf("%s failed: %w (stderr: %s)", cmd.Path, waitErr, stderr.String())
	}
	if copyErr != nil {
		return n, fmt.Errorf("copy dump output: %w", copyErr)
	}
	return n, nil
}
