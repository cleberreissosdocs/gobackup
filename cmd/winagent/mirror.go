// Package main (mirror.go): native file-tree copy engine for the Windows
// agent — no rsync, no Cygwin. Uses Go's own os.File/io.CopyBuffer, which
// on Windows calls the real Win32 ReadFile/WriteFile syscalls directly
// (through the Go runtime) with no POSIX-emulation translation layer in
// between, unlike a Cygwin-built rsync.exe would. Concurrency comes from a
// goroutine worker pool copying multiple files at once (the same idea as
// robocopy's /MT), tuned to the number of logical CPUs.
//
// This is plain, portable Go (no windows-only syscalls), so it also
// compiles and runs correctly on Linux/macOS — useful for testing this
// logic without a Windows box at all, even though the only real deployment
// target is Windows.
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// copyBufSize: larger than the default io.Copy buffer (32KB) to cut down
// syscall overhead per MB transferred, a meaningful win for big files.
const copyBufSize = 4 * 1024 * 1024

// MirrorProgress is called periodically during a run so the caller can
// surface live progress (e.g. into the heartbeat's RunningInfo).
type MirrorProgress func(filesDone, filesTotal int, bytesDone int64, currentFile string)

// MirrorResult summarizes a finished run.
type MirrorResult struct {
	FilesCopied  int
	BytesCopied  int64
	FilesDeleted int
	FilesSkipped int // already up to date, nothing to do
}

// Mirror copies everything under src into dst, skipping files that are
// already identical by size+modtime (the same quick-check heuristic rsync
// uses by default), and — if deleteExtraneous — removes anything under dst
// that no longer exists under src, so dst ends up an exact mirror of src.
func Mirror(src, dst string, deleteExtraneous bool, onProgress MirrorProgress) (MirrorResult, error) {
	src = filepath.Clean(src)
	dst = filepath.Clean(dst)

	srcInfo, err := os.Stat(src)
	if err != nil {
		return MirrorResult{}, fmt.Errorf("stat source: %w", err)
	}
	if !srcInfo.IsDir() {
		return MirrorResult{}, fmt.Errorf("source is not a directory: %s", src)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return MirrorResult{}, fmt.Errorf("create destination: %w", err)
	}

	type fileJob struct {
		rel     string
		srcPath string
		dstPath string
		size    int64
	}

	var jobs []fileJob
	seenRel := make(map[string]bool)

	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		seenRel[rel] = true
		dstPath := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		jobs = append(jobs, fileJob{rel: rel, srcPath: path, dstPath: dstPath, size: info.Size()})
		return nil
	})
	if err != nil {
		return MirrorResult{}, fmt.Errorf("walk source: %w", err)
	}

	// Decide which files actually need copying (quick-check: size+mtime).
	var toCopy []fileJob
	skipped := 0
	for _, j := range jobs {
		srcStat, _ := os.Stat(j.srcPath)
		dstStat, statErr := os.Stat(j.dstPath)
		if statErr == nil && srcStat != nil && dstStat.Size() == srcStat.Size() && !dstStat.ModTime().Before(srcStat.ModTime()) {
			skipped++
			continue
		}
		toCopy = append(toCopy, j)
	}

	var (
		copiedCount int64
		copiedBytes int64
		mu          sync.Mutex
		firstErr    error
	)
	total := len(toCopy)
	workers := runtime.NumCPU()
	if workers < 2 {
		workers = 2
	}
	if workers > total && total > 0 {
		workers = total
	}

	jobCh := make(chan fileJob)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobCh {
				n, copyErr := copyOneFile(j.srcPath, j.dstPath)
				mu.Lock()
				if copyErr != nil && firstErr == nil {
					firstErr = fmt.Errorf("copy %s: %w", j.rel, copyErr)
				}
				mu.Unlock()
				atomic.AddInt64(&copiedBytes, n)
				done := atomic.AddInt64(&copiedCount, 1)
				if onProgress != nil {
					onProgress(int(done), total, atomic.LoadInt64(&copiedBytes), j.rel)
				}
			}
		}()
	}
	for _, j := range toCopy {
		jobCh <- j
	}
	close(jobCh)
	wg.Wait()

	if firstErr != nil {
		return MirrorResult{FilesCopied: int(copiedCount), BytesCopied: copiedBytes, FilesSkipped: skipped}, firstErr
	}

	deleted := 0
	if deleteExtraneous {
		deleted, err = pruneExtraneous(dst, src, seenRel)
		if err != nil {
			return MirrorResult{FilesCopied: int(copiedCount), BytesCopied: copiedBytes, FilesSkipped: skipped}, fmt.Errorf("prune extraneous: %w", err)
		}
	}

	return MirrorResult{
		FilesCopied:  int(copiedCount),
		BytesCopied:  copiedBytes,
		FilesDeleted: deleted,
		FilesSkipped: skipped,
	}, nil
}

func copyOneFile(srcPath, dstPath string) (int64, error) {
	in, err := os.Open(srcPath)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return 0, err
	}
	// Write to a temp file then rename, so a crash/interrupt mid-copy never
	// leaves a half-written file masquerading as a finished one.
	tmpPath := dstPath + ".gobackup-tmp"
	out, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	buf := make([]byte, copyBufSize)
	n, copyErr := io.CopyBuffer(out, in, buf)
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(tmpPath)
		return 0, copyErr
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return 0, closeErr
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		os.Remove(tmpPath)
		return 0, err
	}
	if srcInfo, statErr := os.Stat(srcPath); statErr == nil {
		_ = os.Chtimes(dstPath, time.Now(), srcInfo.ModTime())
	}
	return n, nil
}

// pruneExtraneous removes anything under dst whose source-relative path is
// not in seenRel — the "--delete" mirror behavior.
func pruneExtraneous(dst, src string, seenRel map[string]bool) (int, error) {
	deleted := 0
	var toRemove []string
	err := filepath.WalkDir(dst, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dst, path)
		if relErr != nil || rel == "." {
			return relErr
		}
		if !seenRel[rel] {
			toRemove = append(toRemove, path)
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, p := range toRemove {
		if rmErr := os.RemoveAll(p); rmErr == nil {
			deleted++
		}
	}
	return deleted, nil
}
