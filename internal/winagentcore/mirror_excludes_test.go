package winagentcore

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func writeFile(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func listFiles(t *testing.T, root string) []string {
	t.Helper()
	var got []string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		got = append(got, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(got)
	return got
}

// TestMirrorExcludes mirrors the identical scenario asserted against the
// REAL rsync binary in internal/backup/excludes_rsync_test.go. Both must
// produce the same file list from the same patterns — that equivalence is
// the whole point of sharing core.ExcludeMatcher's documented semantics,
// since a user writing one exclusion list should not get different results
// depending on whether the agent is the Linux or the Windows one.
func TestMirrorExcludes(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	for _, rel := range []string{
		"keep.txt", "scratch.tmp",
		"sub/keep2.txt", "sub/also.tmp",
		"cache/big.bin", "sub/cache/nested.bin",
		"logs/app.gz",
		"topo/x.txt", "sub/topo/y.txt",
	} {
		writeFile(t, src, rel)
	}

	_, err := Mirror(context.Background(), src, dst, false,
		[]string{"*.tmp", "cache/", "/topo", "logs/*.gz"}, nil, nil)
	if err != nil {
		t.Fatalf("Mirror failed: %v", err)
	}

	got := listFiles(t, dst)
	want := []string{"keep.txt", "sub/keep2.txt", "sub/topo/y.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("copied %v, want %v", got, want)
	}
}

// TestMirrorExcludesAreNotPruned is the Windows-side counterpart of
// TestRunExcludesDoNotDeleteAtDestination: turning on an exclusion must
// never make the mirror delete what an earlier run already copied.
func TestMirrorExcludesAreNotPruned(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, src, "keep.txt")
	writeFile(t, dst, "old.tmp")
	writeFile(t, dst, "cache/previously-copied.bin")

	if _, err := Mirror(context.Background(), src, dst, true,
		[]string{"*.tmp", "cache/"}, nil, nil); err != nil {
		t.Fatalf("Mirror failed: %v", err)
	}

	for _, rel := range []string{"old.tmp", "cache/previously-copied.bin"} {
		if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(rel))); err != nil {
			t.Errorf("excluded path %q must survive --delete pruning: %v", rel, err)
		}
	}
}

// TestMirrorStillPrunesNonExcluded guards the opposite mistake: making
// exclusions protective must not accidentally disable mirroring deletions
// for everything else.
func TestMirrorStillPrunesNonExcluded(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, src, "keep.txt")
	writeFile(t, dst, "gone.txt")

	if _, err := Mirror(context.Background(), src, dst, true, []string{"*.tmp"}, nil, nil); err != nil {
		t.Fatalf("Mirror failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "gone.txt")); err == nil {
		t.Error("a non-excluded file missing from the source should still be pruned")
	}
}
