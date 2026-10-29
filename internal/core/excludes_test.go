package core

import "testing"

// Comments survive parsing (so they survive an edit-and-save round trip)
// and are skipped only where patterns are applied — see
// TestExcludeMatcherEmptyAndBadPatterns for the matcher side, and
// internal/backup's rsync args for the Linux agent side.
func TestParseExcludePatterns(t *testing.T) {
	got := ParseExcludePatterns("*.tmp\r\n\n  cache/  \n# comentário\n/logs\n")
	want := []string{"*.tmp", "cache/", "# comentário", "/logs"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestExcludeMatcher(t *testing.T) {
	m := NewExcludeMatcher([]string{
		"*.tmp",
		"cache/",
		"/topo",
		"logs/*.gz",
		"**/node_modules",
	})

	cases := []struct {
		rel   string
		isDir bool
		want  bool
		why   string
	}{
		{"a.tmp", false, true, "basename glob at root"},
		{"x/y/a.tmp", false, true, "basename glob at depth"},
		{"a.tmpx", false, false, "glob must match the whole name"},
		{"cache", true, true, "dir-only pattern matches a directory"},
		{"cache", false, false, "dir-only pattern must not match a file"},
		{"x/cache", true, true, "dir-only pattern floats to any depth"},
		{"topo", true, true, "anchored pattern at the root"},
		{"x/topo", true, false, "anchored pattern must not match deeper"},
		{"logs/err.gz", false, true, "slash pattern, anchored at root"},
		{"app/logs/err.gz", false, true, "slash pattern floats to any depth"},
		{"logs/err.txt", false, false, "slash pattern respects the extension"},
		{"a/b/node_modules", true, true, "** crosses separators"},
		{"node_modules", true, true, "** also matches at the root"},
		{"src/main.go", false, false, "unrelated path is kept"},
	}
	for _, c := range cases {
		if got := m.Match(c.rel, c.isDir); got != c.want {
			t.Errorf("Match(%q, dir=%v) = %v, want %v (%s)", c.rel, c.isDir, got, c.want, c.why)
		}
	}
}

func TestExcludedCoversSubtree(t *testing.T) {
	m := NewExcludeMatcher([]string{"cache/"})
	if !m.Excluded("x/cache/deep/file.bin", false) {
		t.Fatal("a file under an excluded directory must itself be excluded")
	}
	if m.Excluded("x/cached/file.bin", false) {
		t.Fatal("a similarly-named sibling directory must not be excluded")
	}
}

func TestExcludeMatcherEmptyAndBadPatterns(t *testing.T) {
	if !NewExcludeMatcher(nil).Empty() {
		t.Fatal("no patterns should be Empty")
	}
	if !NewExcludeMatcher([]string{"", "   ", "# só comentário", "/"}).Empty() {
		t.Fatal("blank/comment-only patterns should produce an empty matcher")
	}
	// A typo must be skipped, never panic or exclude everything.
	m := NewExcludeMatcher([]string{"[", "*.tmp"})
	if !m.Match("a.tmp", false) {
		t.Fatal("a valid pattern alongside a broken one must still work")
	}
	if m.Match("a.txt", false) {
		t.Fatal("a broken pattern must not match everything")
	}
}
