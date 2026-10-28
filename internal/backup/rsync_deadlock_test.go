package backup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestRun_DoesNotDeadlockOnChattyCRProgress reproduces the real-world
// deadlock found on a G1 file-mirror job over a directory with millions of
// entries: a fake "rsync" that prints hundreds of KB of '\r'-only progress
// updates (no '\n') before its first real newline, well past the
// scanner's old 1MB single-token limit. Before the fix (plain
// bufio.ScanLines), this test hung for the full context timeout because
// Scan() silently stopped draining stdout once a token exceeded the
// buffer, and the fake rsync blocked forever writing its next update into
// the now-full, unread pipe -- exactly what was observed live (all rsync
// processes stuck in pipe_write/do_select, near-zero CPU, forever).
func TestRun_DoesNotDeadlockOnChattyCRProgress(t *testing.T) {
	fakeBin := t.TempDir()
	script := `#!/bin/sh
i=0
while [ $i -lt 50000 ]; do
  printf '\r%d%% some/deep/path/file%d.bin' "$((i % 100))" "$i"
  i=$((i + 1))
done
printf '\n100%% 12345 bytes done\n'
echo 'Number of files transferred: 1'
echo 'Total transferred file size: 12345 bytes'
exit 0
`
	rsyncPath := filepath.Join(fakeBin, "rsync")
	if err := os.WriteFile(rsyncPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake rsync: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+origPath)
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Fatalf("fake rsync not on PATH: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan struct{})
	var progressCalls int
	var runErr error
	go func() {
		defer close(done)
		_, runErr = Run(ctx, t.TempDir(), Local(t.TempDir()), "full", "", false,
			func(percent int, line string) { progressCalls++ }, nil)
	}()

	select {
	case <-done:
		if runErr != nil {
			t.Fatalf("Run returned an error: %v", runErr)
		}
		if progressCalls == 0 {
			t.Fatal("onProgress was never called -- progress lines weren't being parsed")
		}
		t.Logf("OK: Run finished normally, onProgress fired %d times", progressCalls)
	case <-ctx.Done():
		t.Fatal("Run HUNG past the context deadline -- the pipe-starvation deadlock is back")
	}
}
