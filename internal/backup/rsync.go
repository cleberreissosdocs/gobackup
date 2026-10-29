// Package backup orchestrates file-tree backups using rsync.
package backup

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/cleber/gobackup/internal/core"
)

// Destination describes where rsync should write the backup. For
// local/nas it is simply a filesystem path (NAS shares are expected to
// already be mounted by the OS — GoBackup treats a mounted NAS the same as
// any local directory). For ssh it is a remote host reachable over SSH,
// and rsync itself handles the transfer securely over that channel.
type Destination struct {
	Type core.DestinationType

	// Type == local or nas
	Path string

	// Type == ssh
	SSHHost    string
	SSHPort    int
	SSHUser    string
	SSHKeyPath string
	SSHPath    string
}

// Local is a convenience constructor for the common same-host case.
func Local(path string) Destination {
	return Destination{Type: core.DestinationLocal, Path: path}
}

// NAS is a convenience constructor for a pre-mounted NAS share.
func NAS(mountedPath string) Destination {
	return Destination{Type: core.DestinationNAS, Path: mountedPath}
}

// SSH is a convenience constructor for a remote rsync-over-ssh destination.
func SSH(host string, port int, user, keyPath, remotePath string) Destination {
	if port == 0 {
		port = 22
	}
	return Destination{
		Type:       core.DestinationSSH,
		SSHHost:    host,
		SSHPort:    port,
		SSHUser:    user,
		SSHKeyPath: keyPath,
		SSHPath:    remotePath,
	}
}

// rsyncArgs returns the rsync destination argument and any extra flags
// (e.g. -e "ssh ...") needed to reach it.
func (d Destination) rsyncArgs() (target string, extraArgs []string, err error) {
	switch d.Type {
	case core.DestinationLocal, core.DestinationNAS, "":
		if d.Path == "" {
			return "", nil, fmt.Errorf("destination path is required for type %q", d.Type)
		}
		return d.Path, nil, nil

	case core.DestinationSSH:
		if d.SSHHost == "" || d.SSHUser == "" || d.SSHPath == "" {
			return "", nil, fmt.Errorf("ssh destination requires host, user and path")
		}
		sshCmd := fmt.Sprintf("ssh -p %d -o StrictHostKeyChecking=accept-new", d.SSHPort)
		if d.SSHKeyPath != "" {
			sshCmd += " -i " + d.SSHKeyPath
		}
		target = fmt.Sprintf("%s@%s:%s", d.SSHUser, d.SSHHost, d.SSHPath)
		extraArgs = []string{"-e", sshCmd}
		return target, extraArgs, nil

	default:
		return "", nil, fmt.Errorf("unsupported destination type: %q", d.Type)
	}
}

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
var percentRe = regexp.MustCompile(`(\d+)%`)

// ProgressFunc is called for each progress line rsync prints (thanks to
// --info=progress2). percent is -1 when the line carries no percentage.
type ProgressFunc func(percent int, line string)

// scanLinesOrCR is a bufio.SplitFunc that treats a bare '\r' as a line
// terminator, same as '\n' (collapsing '\r\n' into a single terminator,
// matching bufio.ScanLines' own convention).
//
// rsync's --info=progress2 redraws its progress line IN PLACE using '\r'
// alone (never '\n') for every intermediate update, only emitting a real
// '\n' when a file finishes or at the very end — exactly how a terminal
// progress bar works. bufio.ScanLines only splits on '\n', so without this
// every single '\r' update kept accumulating into ONE ever-growing
// "line" until the scanner's buffer cap was hit, Scan() silently returned
// false (ErrTooLong, never checked), and the loop stopped draining
// rsync's stdout pipe entirely. rsync then blocked forever trying to
// WRITE its next progress update into the now-full, unread pipe —
// confirmed live on a real job with enough directory churn to produce far
// more than a buffer's worth of '\r' updates before its first real
// newline: all three rsync processes (generator/sender/receiver) stuck in
// "pipe_write"/"do_select", essentially zero CPU used for over an hour,
// progress stuck at 0% forever since onProgress was never being called
// again either.
func scanLinesOrCR(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
			return i + 2, data[:i], nil
		}
		return i + 1, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// progress2LineRe parses rsync --info=progress2's aggregate progress line,
// e.g.:
//
//	8,062,193,286  2%  136.33MB/s    0:40:48 (xfr#153, to-chk=1234543/9876543)
//
// Fields (per rsync's own docs): total bytes transferred so far, percent of
// the estimated total, current transfer rate, and elapsed time since the
// transfer started -- NOT files transferred, and NOT remaining time, a
// common misreading of that 4th column.
var progress2LineRe = regexp.MustCompile(`^\s*([\d,]+)\s+(\d+)%\s+(\S+/s)\s+([\d:]+)(?:\s+\(xfr#(\d+), (?:ir|to)-chk=(\d+)/(\d+)\))?\s*$`)

// humanizeProgressLine turns rsync's raw, column-aligned progress2 line
// (meant for a terminal, not an end user) into a short human sentence for
// the web UI's progress message -- e.g. a user reported seeing the literal
// "8,062,193,286  2%  136.33MB/s    0:40:48" and understandably couldn't
// tell that was a percentage at all. Falls back to the raw line unchanged
// for anything that isn't a progress2 column line (per-file "building file
// list..."/summary lines, warnings, etc.), so nothing is ever hidden.
func humanizeProgressLine(line string) string {
	m := progress2LineRe.FindStringSubmatch(line)
	if m == nil {
		return line
	}
	bytesDone := parseCount(m[1])
	percent, rate, elapsed := m[2], m[3], m[4]
	msg := fmt.Sprintf("%s%% concluído — %s transferidos — %s — decorrido %s", percent, humanizeBytes(bytesDone), rate, elapsed)
	if m[5] != "" && m[7] != "" {
		msg += fmt.Sprintf(" — %s de %s arquivos restantes", m[6], m[7])
	}
	return msg
}

func humanizeBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// Run executes an rsync file-tree backup from sourcePath to dest.
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
// linkDestPath may be empty for a full backup. deleteExtraneous controls
// whether files that exist at the destination but no longer exist at the
// source are removed (rsync's --delete) — i.e. whether the destination
// stays an EXACT mirror of the source, or just accumulates/overwrites
// without ever pruning. excludes is a list of rsync --exclude patterns
// (see core.ExcludeMatcher for the syntax, which the Windows agent
// reimplements to behave identically); nil/empty excludes nothing. Note:
// for an ssh destination, --link-dest is
// resolved on the REMOTE host, so it must be a path on that host, not on
// the agent running rsync. onProgress may be nil. onStart, if non-nil, is
// called with the rsync *os.Process right after it starts — the caller
// uses this to support OS-level Pause/Resume (SIGSTOP/SIGCONT) on top of
// ctx cancellation for outright Cancel.
func Run(ctx context.Context, sourcePath string, dest Destination, backupType core.BackupType, linkDestPath string, deleteExtraneous bool, excludes []string, onProgress ProgressFunc, onStart func(*os.Process)) (Result, error) {
	target, extraArgs, err := dest.rsyncArgs()
	if err != nil {
		return Result{}, fmt.Errorf("invalid destination: %w", err)
	}

	args := []string{
		"-a",      // archive mode: recursive, preserves perms/times/symlinks
		"--stats", // machine-ish parsable summary at the end
		"--info=progress2",
	}
	if deleteExtraneous {
		args = append(args, "--delete") // mirror deletions too
	}
	// --exclude must come BEFORE --delete takes effect on the destination
	// side in the sense that matters here: rsync never deletes an excluded
	// path at the destination (it is simply not part of the transfer), so
	// an exclusion added later leaves whatever was already copied in place
	// rather than wiping it.
	for _, p := range excludes {
		// "#" lines are the user's own notes (see ParseExcludePatterns),
		// not patterns — passing one through would tell rsync to skip a
		// file literally named "# comentário".
		if p = strings.TrimSpace(p); p != "" && !strings.HasPrefix(p, "#") {
			args = append(args, "--exclude="+p)
		}
	}
	args = append(args, extraArgs...)
	if backupType != core.BackupFull && linkDestPath != "" {
		args = append(args, "--link-dest="+linkDestPath)
	}
	args = append(args, ensureTrailingSlash(sourcePath), target)

	cmd := exec.Command("rsync", args...)
	// New process group: rsync (even copying purely locally, no ssh)
	// forks an internal sender/receiver pair, so killing only the PID Go
	// tracks on Cancel would leave that child running and the parent as
	// an unreaped zombie forever. setNewProcessGroup lets us signal the
	// WHOLE group at once below (no-op stub on Windows -- this package
	// only ever gets pulled into a Windows BUILD transitively, never
	// actually run there).
	setNewProcessGroup(cmd)
	var out strings.Builder
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("rsync stdout pipe: %w", err)
	}
	cmd.Stderr = &out

	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("rsync start: %w", err)
	}
	if onStart != nil {
		onStart(cmd.Process)
	}

	// ctx cancellation kills the entire process group (negative PID) —
	// exec.CommandContext's default behavior only kills cmd.Process
	// itself, which doesn't reach rsync's forked children.
	killDone := make(chan struct{})
	defer close(killDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = killProcessGroup(cmd.Process.Pid)
		case <-killDone:
		}
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	scanner.Split(scanLinesOrCR)
	for scanner.Scan() {
		line := scanner.Text()
		out.WriteString(line)
		out.WriteByte('\n')
		if onProgress != nil {
			pct := -1
			if m := percentRe.FindStringSubmatch(line); len(m) == 2 {
				if n, err := strconv.Atoi(m[1]); err == nil {
					pct = n
				}
			}
			onProgress(pct, humanizeProgressLine(line))
		}
	}
	if err := scanner.Err(); err != nil {
		// Not fatal by itself (rsync might still finish and cmd.Wait()
		// below will surface the real failure if any) but worth knowing
		// about if this ever fires again — silently swallowing it is
		// exactly how the progress-line-starvation deadlock below went
		// unnoticed for so long.
		out.WriteString(fmt.Sprintf("\n[gobackup: stopped reading rsync output: %v]\n", err))
	}

	if err := cmd.Wait(); err != nil {
		return Result{RawOutput: out.String()}, summarizeRunError(err, out.String())
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

// unreadablePathRe matches rsync's "could not access this path" errors —
// readlink_stat/readdir/lstat/open etc. all share the same two shapes:
//
//	rsync: [sender] readlink_stat("/path/to/thing") failed: No error information (117)
//	rsync: [sender] readdir("/path/to/dir"): No error information (117)
//
// (errno 117 is EUCLEAN/"Structure needs cleaning" — filesystem-level
// corruption at that exact path, the actual real-world case this was
// written for, but the pattern covers any reason rsync reports this way:
// permission denied, I/O error, stale handle, etc.)
var unreadablePathRe = regexp.MustCompile(`rsync: \[(?:sender|receiver|generator)\] \w+\("([^"]+)"\)(?: failed)?: (.+)`)

// summarizeRunError turns rsync's raw output on failure -- which can be
// MEGABYTES for a job over millions of files/progress2 updates, as
// confirmed live on a real backup (8.9MB stored verbatim as the error
// message, 99.9% of it duplicate progress lines) -- into a short, human
// message. When rsync named specific paths it couldn't read (by far the
// most common real failure: source-side corruption, permissions,
// vanished files), lists just those paths — a user has zero chance of
// spotting 21 relevant lines buried in tens of thousands of progress
// updates, which is exactly what was reported ("um monte de códigos").
// Falls back to rsync's own last summary line for anything else, never
// the raw multi-megabyte dump.
func summarizeRunError(waitErr error, rawOutput string) error {
	matches := unreadablePathRe.FindAllStringSubmatch(rawOutput, -1)
	if len(matches) == 0 {
		return fmt.Errorf("rsync falhou (%s): %s", waitErr, lastMeaningfulLine(rawOutput))
	}

	seen := make(map[string]bool, len(matches))
	paths := make([]string, 0, len(matches))
	for _, m := range matches {
		if !seen[m[1]] {
			seen[m[1]] = true
			paths = append(paths, m[1])
		}
	}

	const maxListed = 30
	var b strings.Builder
	b.WriteString("Alguns arquivos não puderam ser lidos (provável corrupção no disco de origem):")
	for i, p := range paths {
		if i >= maxListed {
			fmt.Fprintf(&b, "\n... e mais %d arquivo(s)", len(paths)-maxListed)
			break
		}
		fmt.Fprintf(&b, "\n- %s", p)
	}
	return errors.New(b.String())
}

// lastMeaningfulLine returns the last non-blank line of rsync's output —
// typically its own "rsync error: ..." summary — instead of the entire
// (potentially huge) capture.
func lastMeaningfulLine(output string) string {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return "(sem saída)"
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
