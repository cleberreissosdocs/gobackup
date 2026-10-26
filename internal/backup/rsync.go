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
// linkDestPath may be empty for a full backup. Note: for an ssh destination,
// --link-dest is resolved on the REMOTE host, so it must be a path on that
// host, not on the agent running rsync.
func Run(ctx context.Context, sourcePath string, dest Destination, backupType core.BackupType, linkDestPath string) (Result, error) {
	target, extraArgs, err := dest.rsyncArgs()
	if err != nil {
		return Result{}, fmt.Errorf("invalid destination: %w", err)
	}

	args := []string{
		"-a",       // archive mode: recursive, preserves perms/times/symlinks
		"--delete", // mirror deletions too
		"--stats",  // machine-ish parsable summary at the end
		"--info=progress2",
	}
	args = append(args, extraArgs...)
	if backupType != core.BackupFull && linkDestPath != "" {
		args = append(args, "--link-dest="+linkDestPath)
	}
	args = append(args, ensureTrailingSlash(sourcePath), target)

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
