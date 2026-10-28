// Package dbdump orchestrates database backups (dump + compression) for
// the five engines GoBackup supports: MariaDB, MySQL, PostgreSQL, SQLite
// and SQL Server (mssql). The dump is written compressed directly to the
// destination directory configured by the user — no uncompressed
// intermediate file (except mssql, which — being a server-side native
// backup — unavoidably writes one temporary .bak file next to the SQL
// Server instance itself; see dumpMSSQL for why and how it's cleaned up).
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
	} else if opts.Engine == core.EngineMSSQL {
		ext = ".bak"
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
	case core.EngineMSSQL:
		written, err = dumpMSSQL(ctx, opts, writer)
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

// dumpMSSQL backs up a single SQL Server / SQL Server Express database via
// sqlcmd's native BACKUP DATABASE statement.
//
// This engine works fundamentally differently from the others: pg_dump and
// mysqldump stream a portable text dump to stdout, so GoBackup can pipe and
// compress it directly without ever touching the database server's own
// disk — including over a real network connection to a remote host.
// SQL Server has no such tool: BACKUP DATABASE is always executed BY the
// SQL Server engine itself, which writes the resulting .bak file to a path
// on ITS OWN filesystem, not the client's. There is no way around this; it
// is how SQL Server backups work natively.
//
// Consequently this only works when the GoBackup agent runs on the SAME
// machine as the SQL Server instance (the common case for SQL Server
// Express, which is typically installed alongside the application it
// serves) — so the path SQL Server writes to is also a path this process
// can read back from. A genuinely remote mssql Host (a separate database
// server reachable only over the network) is not supported: see the error
// below when the produced .bak can't be opened locally.
//
// "All databases" isn't supported either — unlike mysqldump/pg_dumpall,
// SQL Server has no single-file-all-databases backup; each database's
// BACKUP DATABASE produces its own independent .bak. A caller wanting
// every database must create one job per database.
func dumpMSSQL(ctx context.Context, opts Options, w io.Writer) (int64, error) {
	if opts.Database == "" || opts.Database == core.AllDatabasesSentinel {
		return 0, fmt.Errorf("mssql: selecione um banco de dados específico — o SQL Server não tem um backup único de \"todos os bancos\" como o mysqldump/pg_dumpall; crie uma tarefa por banco")
	}

	backupDir, err := mssqlBackupDir(ctx, opts)
	if err != nil {
		return 0, fmt.Errorf("descobrir pasta de backup do SQL Server: %w", err)
	}

	tmpName := fmt.Sprintf("gobackup-%s-%d.bak", opts.Database, time.Now().UnixNano())
	tmpPath := filepath.Join(backupDir, tmpName)

	backupSQL := fmt.Sprintf(
		"SET NOCOUNT ON; BACKUP DATABASE [%s] TO DISK = N'%s' WITH INIT, COPY_ONLY;",
		mssqlEscapeIdent(opts.Database), mssqlEscapePathLiteral(tmpPath),
	)
	cmd := exec.CommandContext(ctx, "sqlcmd", append(sqlcmdConnArgs(opts), "-b", "-Q", backupSQL)...)
	if opts.Password != "" {
		cmd.Env = append(os.Environ(), "SQLCMDPASSWORD="+opts.Password)
	}
	var stderr strings.Builder
	cmd.Stdout = &stderr // sqlcmd prints progress ("10 percent processed") to stdout too; capture both for the error message
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("sqlcmd BACKUP DATABASE falhou: %w (saída: %s)", err, strings.TrimSpace(stderr.String()))
	}
	defer os.Remove(tmpPath)

	f, err := os.Open(tmpPath)
	if err != nil {
		return 0, fmt.Errorf("abrir o .bak que o SQL Server gerou em %q: %w — isso normalmente significa que o agente GoBackup não está rodando na MESMA máquina da instância SQL Server (BACKUP DATABASE sempre escreve no disco do próprio servidor)", tmpPath, err)
	}
	defer f.Close()

	// Chunked copy (not plain io.Copy) so a cancelled backup actually stops
	// reading instead of streaming a multi-GB .bak to completion regardless
	// — same reasoning as dumpSQLite above.
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

// mssqlBackupDir asks the SQL Server instance for its own configured
// default backup directory (set correctly for the SQL Server service
// account's write permissions since install time — reusing it sidesteps
// having to grant that account access to some other folder ourselves).
func mssqlBackupDir(ctx context.Context, opts Options) (string, error) {
	query := "SET NOCOUNT ON; SELECT CAST(SERVERPROPERTY('InstanceDefaultBackupPath') AS NVARCHAR(4000));"
	cmd := exec.CommandContext(ctx, "sqlcmd", append(sqlcmdConnArgs(opts), "-b", "-h", "-1", "-W", "-Q", query)...)
	if opts.Password != "" {
		cmd.Env = append(os.Environ(), "SQLCMDPASSWORD="+opts.Password)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%w (saída: %s)", err, strings.TrimSpace(stderr.String()))
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", fmt.Errorf("a instância não retornou um caminho de backup padrão")
	}
	return dir, nil
}

// sqlcmdConnArgs builds the -S/-U/-E portion shared by every sqlcmd
// invocation. With no Username, "-E" requests Windows/trusted
// authentication (the common case for a co-located SQL Express instance);
// with one, SQL authentication is used and the password is supplied via
// the SQLCMDPASSWORD environment variable by the caller, never on argv.
func sqlcmdConnArgs(opts Options) []string {
	server := "localhost"
	if opts.Host != "" {
		server = opts.Host
	}
	if opts.Port != 0 {
		server = fmt.Sprintf("tcp:%s,%d", server, opts.Port)
	}
	args := []string{"-S", server, "-C"} // -C: trust the server certificate (SQL Express's default self-signed cert otherwise blocks every connection)
	if opts.Username != "" {
		args = append(args, "-U", opts.Username)
	} else {
		args = append(args, "-E")
	}
	return args
}

// mssqlEscapeIdent escapes a database name for use inside a bracketed
// T-SQL identifier ("[name]") — doubling any literal "]" is the one
// character bracketed identifiers need escaped.
func mssqlEscapeIdent(name string) string {
	return strings.ReplaceAll(name, "]", "]]")
}

// mssqlEscapePathLiteral escapes a file path for use inside a T-SQL
// N'...' string literal — doubling any literal single quote.
func mssqlEscapePathLiteral(path string) string {
	return strings.ReplaceAll(path, "'", "''")
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
