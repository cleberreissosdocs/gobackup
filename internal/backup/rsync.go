// Package backup orchestrates file-tree backups using rsync.
package backup

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/cleber/gobackup/internal/core"
)

// Result summarizes a finished rsync run.
type Result struct {
	BytesTransferred int64
	FilesTransferred int64
	RawOutput        string
}

// statsLineRe matches the final summary lines from `rsync --stats`, e.g.:
//
//	Number of files transferred: 12
//	Total transferred file size: 1,048,576 bytes
var numberTransferredRe = regexp.MustCompile(`Number of (?:regular )?files transferred:\s*([\d,]+)`)
var totalBytesRe = regexp.MustCompile(`Total transferred file size:\s*([\d,]+)\s*bytes`)

// Run executes an rsync file-tree backup from source to destination.
//
// backupType controls the rsync flags used:
//   - full:         plain mirror copy (no --link-dest)
//   - incremental:  copies only changes since the last run, hardlinking
//     unchanged files from linkDestPath when provided (space-efficient,
//     similar to rsnapshot).
//   - differential: like incremental but always links against the single
//     baseline (linkDestPath points at the last FULL backup instead of the
//     last run).
//
// linkDestPath may be empty for a full backup.
func Run(ctx context.Context, sourcePath, destPath string, backupType core.BackupType, linkDestPath string) (Result, error) {
	args := []string{
		"-a",             // archive mode: recursive, preserves perms/times/symlinks
		"--delete",       // mirror deletions too
		"--stats",        // machine-ish parsable summary at the end
		"--info=progress2",
	}
	if backupType != core.BackupFull && linkDestPath != "" {
		args = append(args, "--link-dest="+linkDestPath)
	}
	args = append(args, ensureTrailingSlash(sourcePath), destPath)

	cmd := exec.CommandContext(ctx, "rsync", args...)
	var out strings.Builder
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("rsync stdout pipe: %w", err)
	}
	cmd.Stderr = &out

	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("rsync start: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		out.WriteString(line)
		out.WriteByte('\n')
	}

	if err := cmd.Wait(); err != nil {
		return Result{RawOutput: out.String()}, fmt.Errorf("rsync failed: %w (output: %s)", err, out.String())
	}

	res := Result{RawOutput: out.String()}
	if m := numberTransferredRe.FindStringSubmatch(out.String()); len(m) == 2 {
		res.FilesTransferred = parseCount(m[1])
	}
	if m := totalBytesRe.FindStringSubmatch(out.String()); len(m) == 2 {
		res.BytesTransferred = parseCount(m[1])
	}
	return res, nil
}

func ensureTrailingSlash(p string) string {
	if strings.HasSuffix(p, "/") {
		return p
	}
	return p + "/"
}

func parseCount(s string) int64 {
	s = strings.ReplaceAll(s, ",", "")
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
