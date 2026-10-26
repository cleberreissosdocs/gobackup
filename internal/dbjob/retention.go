package dbjob

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cleber/gobackup/internal/dbdump"
)

// CleanupOld removes this job's own past backup files (matched by the
// standard "{engine}-{database}-" naming prefix, so unrelated files sharing
// the destination directory are never touched) older than retentionDays.
// retentionDays <= 0 means "keep forever" — cleanup is a no-op.
func CleanupOld(ctx context.Context, req Request, retentionDays int) (deleted int, err error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	prefix := dbdump.NamePrefix(dbdump.Options{Engine: req.Engine, Database: req.Database, SQLitePath: req.SQLitePath})
	destType := req.DestType
	if destType == "" {
		destType = "local"
	}
	switch destType {
	case "local", "nas":
		return cleanupLocal(req.DestPath, prefix, retentionDays)
	case "ssh":
		return cleanupSSH(ctx, req.SSHHost, req.SSHPort, req.SSHUser, req.SSHKey, req.DestPath, prefix, retentionDays)
	default:
		return 0, nil
	}
}

func cleanupLocal(destDir, prefix string, retentionDays int) (int, error) {
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	entries, err := os.ReadDir(destDir)
	if err != nil {
		return 0, fmt.Errorf("list destination for retention cleanup: %w", err)
	}
	deleted := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if rmErr := os.Remove(filepath.Join(destDir, e.Name())); rmErr == nil {
				deleted++
			}
		}
	}
	return deleted, nil
}

// cleanupSSH runs a remote `find ... -delete` over ssh (same transport
// rsync already uses for the backup itself), since the destination
// directory lives on another host entirely.
func cleanupSSH(ctx context.Context, host string, port int, user, keyPath, remoteDir, prefix string, retentionDays int) (int, error) {
	if port == 0 {
		port = 22
	}
	// -mtime +N matches files last modified more than N days ago (rounds
	// down to whole days, so this is an approximation of "older than N
	// days", not down-to-the-second precise).
	remoteCmd := fmt.Sprintf(
		"find %s -maxdepth 1 -type f -name %s -mtime +%d -print -delete",
		shQuote(remoteDir), shQuote(prefix+"*"), retentionDays,
	)
	args := []string{"-p", strconv.Itoa(port), "-o", "StrictHostKeyChecking=accept-new"}
	if keyPath != "" {
		args = append(args, "-i", keyPath)
	}
	args = append(args, fmt.Sprintf("%s@%s", user, host), remoteCmd)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ssh retention cleanup: %w", err)
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return 0, nil
	}
	return len(strings.Split(trimmed, "\n")), nil
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
