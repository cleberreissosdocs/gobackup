// Package main (sshdest.go): native SSH/SFTP transport for the "ssh"
// destination type, used by both file-mirror and database-dump jobs. Pure
// Go (golang.org/x/crypto/ssh + github.com/pkg/sftp) — no dependency on an
// external ssh.exe/scp.exe/rsync.exe being installed on the Windows box,
// unlike the Linux agent's internal/backup package which shells out to
// rsync over ssh.
package winagentcore

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// sshConnect opens an SSH+SFTP session authenticated with a private key
// file. GoBackup only ever uses key-based auth for SSH destinations (same
// as the Linux agent), never a password.
func sshConnect(host string, port int, user, keyPath string) (*ssh.Client, *sftp.Client, error) {
	if port == 0 {
		port = 22
	}
	if keyPath == "" {
		return nil, nil, fmt.Errorf("ssh_key_path é obrigatório para destino SSH")
	}
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("ler chave SSH: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		return nil, nil, fmt.Errorf("parsear chave SSH: %w", err)
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // same trust model as the Linux agent's "StrictHostKeyChecking=accept-new"
		Timeout:         15 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("conectar SSH em %s: %w", addr, err)
	}
	sc, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("abrir sessão SFTP: %w", err)
	}
	return client, sc, nil
}

// UploadFile sends a single local file into a remote directory (creating it
// if needed), used by database-dump jobs with an SSH destination.
func UploadFile(host string, port int, user, keyPath, localPath, remoteDir string) (int64, error) {
	client, sc, err := sshConnect(host, port, user, keyPath)
	if err != nil {
		return 0, err
	}
	defer client.Close()
	defer sc.Close()

	remoteDir = toRemoteSlashes(remoteDir)
	if err := sc.MkdirAll(remoteDir); err != nil {
		return 0, fmt.Errorf("criar diretório remoto: %w", err)
	}

	in, err := os.Open(localPath)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	remotePath := path.Join(remoteDir, filepath.Base(localPath))
	tmpRemote := remotePath + ".gobackup-tmp"
	out, err := sc.Create(tmpRemote)
	if err != nil {
		return 0, fmt.Errorf("criar arquivo remoto: %w", err)
	}
	n, copyErr := io.Copy(out, in)
	out.Close()
	if copyErr != nil {
		sc.Remove(tmpRemote)
		return 0, copyErr
	}
	if err := sc.Rename(tmpRemote, remotePath); err != nil {
		// Some SFTP servers refuse to rename over an existing target —
		// remove-then-rename as a fallback.
		_ = sc.Remove(remotePath)
		if err2 := sc.Rename(tmpRemote, remotePath); err2 != nil {
			sc.Remove(tmpRemote)
			return 0, fmt.Errorf("renomear arquivo remoto: %w", err2)
		}
	}
	return n, nil
}

// MirrorSSH is the SSH-destination equivalent of Mirror() (mirror.go): walks
// the local source tree and uploads anything missing/changed on the remote
// side (size+mtime quick-check), optionally deleting remote files that no
// longer exist locally.
func MirrorSSH(ctx context.Context, src, host string, port int, user, keyPath, remoteDest string, deleteExtraneous bool, onProgress MirrorProgress, waitIfPaused func(context.Context)) (MirrorResult, error) {
	client, sc, err := sshConnect(host, port, user, keyPath)
	if err != nil {
		return MirrorResult{}, err
	}
	defer client.Close()
	defer sc.Close()

	src = filepath.Clean(src)
	remoteDest = toRemoteSlashes(remoteDest)
	if err := sc.MkdirAll(remoteDest); err != nil {
		return MirrorResult{}, fmt.Errorf("criar diretório remoto: %w", err)
	}

	type fileJob struct {
		rel        string
		srcPath    string
		remotePath string
		size       int64
	}
	var jobs []fileJob
	seenRel := make(map[string]bool)

	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, p)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		seenRel[relSlash] = true
		remotePath := path.Join(remoteDest, relSlash)
		if d.IsDir() {
			return sc.MkdirAll(remotePath)
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		jobs = append(jobs, fileJob{rel: relSlash, srcPath: p, remotePath: remotePath, size: info.Size()})
		return nil
	})
	if err != nil {
		return MirrorResult{}, fmt.Errorf("percorrer origem: %w", err)
	}

	var toCopy []fileJob
	skipped := 0
	for _, j := range jobs {
		srcStat, statErr := os.Stat(j.srcPath)
		remoteInfo, remoteErr := sc.Stat(j.remotePath)
		// SFTP (unlike the local filesystem) only carries whole-second
		// mtime precision, so compare truncated to the second — otherwise
		// a local mtime with sub-second fractions always looks "newer"
		// than the remote one and every file gets re-uploaded every run.
		if statErr == nil && remoteErr == nil && remoteInfo.Size() == srcStat.Size() &&
			!remoteInfo.ModTime().Before(srcStat.ModTime().Truncate(time.Second)) {
			skipped++
			continue
		}
		toCopy = append(toCopy, j)
	}

	var copiedCount int
	var copiedBytes int64
	total := len(toCopy)
	for _, j := range toCopy {
		select {
		case <-ctx.Done():
			return MirrorResult{FilesCopied: copiedCount, BytesCopied: copiedBytes, FilesSkipped: skipped}, ctx.Err()
		default:
		}
		if waitIfPaused != nil {
			waitIfPaused(ctx)
		}
		n, copyErr := uploadOne(sc, j.srcPath, j.remotePath)
		if copyErr != nil {
			return MirrorResult{FilesCopied: copiedCount, BytesCopied: copiedBytes, FilesSkipped: skipped}, fmt.Errorf("copiar %s: %w", j.rel, copyErr)
		}
		copiedBytes += n
		copiedCount++
		if onProgress != nil {
			onProgress(copiedCount, total, copiedBytes, j.rel)
		}
	}

	deleted := 0
	if deleteExtraneous {
		deleted, err = pruneExtraneousSSH(sc, remoteDest, seenRel)
		if err != nil {
			return MirrorResult{FilesCopied: copiedCount, BytesCopied: copiedBytes, FilesSkipped: skipped}, fmt.Errorf("remover excedente remoto: %w", err)
		}
	}

	return MirrorResult{FilesCopied: copiedCount, BytesCopied: copiedBytes, FilesDeleted: deleted, FilesSkipped: skipped}, nil
}

func uploadOne(sc *sftp.Client, localPath, remotePath string) (int64, error) {
	in, err := os.Open(localPath)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	if err := sc.MkdirAll(path.Dir(remotePath)); err != nil {
		return 0, err
	}
	tmpRemote := remotePath + ".gobackup-tmp"
	out, err := sc.Create(tmpRemote)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(out, in)
	out.Close()
	if copyErr != nil {
		sc.Remove(tmpRemote)
		return 0, copyErr
	}
	_ = sc.Remove(remotePath)
	if err := sc.Rename(tmpRemote, remotePath); err != nil {
		sc.Remove(tmpRemote)
		return 0, err
	}
	if srcInfo, statErr := os.Stat(localPath); statErr == nil {
		_ = sc.Chtimes(remotePath, time.Now(), srcInfo.ModTime())
	}
	return n, nil
}

func pruneExtraneousSSH(sc *sftp.Client, remoteDest string, seenRel map[string]bool) (int, error) {
	deleted := 0
	walker := sc.Walk(remoteDest)
	var toRemove []string
	for walker.Step() {
		if walker.Err() != nil {
			continue
		}
		p := walker.Path()
		if p == remoteDest {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, remoteDest), "/")
		if !seenRel[rel] {
			toRemove = append(toRemove, p)
			if walker.Stat().IsDir() {
				walker.SkipDir()
			}
		}
	}
	for _, p := range toRemove {
		if err := sc.RemoveAll(p); err == nil {
			deleted++
		}
	}
	return deleted, nil
}

// CleanupOldSSH removes this job's own past backup files (matched by
// prefix) older than retentionDays, over SFTP — the SSH-destination
// equivalent of internal/dbjob's cleanupLocal, used for database jobs.
func CleanupOldSSH(host string, port int, user, keyPath, remoteDir, prefix string, retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	client, sc, err := sshConnect(host, port, user, keyPath)
	if err != nil {
		return 0, err
	}
	defer client.Close()
	defer sc.Close()

	remoteDir = toRemoteSlashes(remoteDir)
	entries, err := sc.ReadDir(remoteDir)
	if err != nil {
		return 0, fmt.Errorf("listar destino remoto: %w", err)
	}
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	deleted := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		if e.ModTime().Before(cutoff) {
			if err := sc.Remove(path.Join(remoteDir, e.Name())); err == nil {
				deleted++
			}
		}
	}
	return deleted, nil
}

// toRemoteSlashes normalizes a destination path typed with Windows-style
// backslashes (easy to type by habit) into the forward-slash form every
// SSH/SFTP server (Linux/NAS) actually expects.
func toRemoteSlashes(p string) string {
	return filepath.ToSlash(p)
}
