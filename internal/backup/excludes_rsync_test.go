package backup

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestRunExcludesRealRsync drives the REAL rsync binary over a real
// directory tree: the exclusion patterns have to survive being turned into
// --exclude flags and be interpreted by rsync itself, which is the only
// thing that proves the feature works end to end (a unit test of the
// matcher proves the Windows agent's copy, not this one).
func TestRunExcludesRealRsync(t *testing.T) {
	if _, err := os.Stat("/usr/bin/rsync"); err != nil {
		t.Skip("rsync not installed")
	}

	src := t.TempDir()
	dst := t.TempDir()

	mustWrite := func(rel string) {
		t.Helper()
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("keep.txt")
	mustWrite("scratch.tmp")
	mustWrite("sub/keep2.txt")
	mustWrite("sub/also.tmp")
	mustWrite("cache/big.bin")
	mustWrite("sub/cache/nested.bin")
	mustWrite("logs/app.gz")
	mustWrite("topo/x.txt")
	mustWrite("sub/topo/y.txt")

	_, err := Run(context.Background(), src, Local(dst), "full", "", false,
		[]string{"*.tmp", "cache/", "/topo", "logs/*.gz"}, nil, nil)
	if err != nil {
		t.Fatalf("rsync failed: %v", err)
	}

	var got []string
	filepath.Walk(dst, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dst, p)
		got = append(got, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(got)

	want := []string{"keep.txt", "sub/keep2.txt", "sub/topo/y.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("copied %v, want %v", got, want)
	}
}

// TestRunIgnoresCommentLines proves a "#" note in the exclusions field is
// treated as a comment by the real rsync invocation, not as a pattern
// matching a file literally named "# ...".
func TestRunIgnoresCommentLines(t *testing.T) {
	if _, err := os.Stat("/usr/bin/rsync"); err != nil {
		t.Skip("rsync not installed")
	}

	src := t.TempDir()
	dst := t.TempDir()
	for _, name := range []string{"keep.txt", "# nota.txt"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := Run(context.Background(), src, Local(dst), "full", "", false,
		[]string{"# isto é um comentário, não um padrão", "*.tmp"}, nil, nil); err != nil {
		t.Fatalf("rsync failed: %v", err)
	}

	for _, name := range []string{"keep.txt", "# nota.txt"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Errorf("a comment line must not exclude anything, %q missing: %v", name, err)
		}
	}
}

// TestRunExcludesDoNotDeleteAtDestination pins rsync's default behavior
// that GoBackup relies on: adding an exclusion must not cause --delete to
// wipe out data a previous run already copied.
func TestRunExcludesDoNotDeleteAtDestination(t *testing.T) {
	if _, err := os.Stat("/usr/bin/rsync"); err != nil {
		t.Skip("rsync not installed")
	}

	src := t.TempDir()
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pretend a previous run (before the exclusion existed) had copied this.
	if err := os.WriteFile(filepath.Join(dst, "old.tmp"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(context.Background(), src, Local(dst), "full", "", true,
		[]string{"*.tmp"}, nil, nil); err != nil {
		t.Fatalf("rsync failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dst, "old.tmp")); err != nil {
		t.Error("an excluded file already at the destination must be left alone by --delete, got: " + err.Error())
	}
}
