package backup

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSummarizeRunError_UnreadablePaths(t *testing.T) {
	raw := `  8,062,193,286  2%  136.33MB/s    0:40:48
rsync: [sender] readlink_stat("/mnt/repositorio/KODAK/.../log") failed: No error information (117)
rsync: [sender] readdir("/mnt/repositorio/KODAK/.../00000051"): No error information (117)
rsync: [sender] readlink_stat("/mnt/repositorio/KODAK/.../info") failed: No error information (117)
Number of files: 550,919
rsync error: some files/attrs were not transferred (see previous errors) (code 23) at main.c(1347) [sender=3.4.3]
`
	err := summarizeRunError(errors.New("exit status 23"), raw)
	got := err.Error()
	t.Logf("got:\n%s", got)

	if !strings.Contains(got, "Alguns arquivos não puderam ser lidos") {
		t.Fatalf("missing human header, got: %s", got)
	}
	if !strings.Contains(got, "/mnt/repositorio/KODAK/.../log") {
		t.Fatalf("missing first path, got: %s", got)
	}
	if !strings.Contains(got, "/mnt/repositorio/KODAK/.../00000051") {
		t.Fatalf("missing readdir-style path, got: %s", got)
	}
	if strings.Contains(got, "8,062,193,286") || strings.Contains(got, "136.33MB/s") {
		t.Fatalf("progress noise leaked into the summary: %s", got)
	}
	if strings.Count(got, "/mnt/repositorio") != 3 {
		t.Fatalf("expected exactly 3 distinct paths, got: %s", got)
	}
}

func TestSummarizeRunError_CapsLongLists(t *testing.T) {
	var raw strings.Builder
	for i := 0; i < 50; i++ {
		raw.WriteString(fmt.Sprintf(`rsync: [sender] readlink_stat("/path/file%d") failed: No error information (117)`, i) + "\n")
	}
	err := summarizeRunError(errors.New("exit status 23"), raw.String())
	if !strings.Contains(err.Error(), "e mais") {
		t.Fatalf("expected a truncation notice for a long list, got: %s", err.Error())
	}
}

func TestSummarizeRunError_FallsBackWhenNoKnownPattern(t *testing.T) {
	raw := "building file list...\nrsync error: something unrelated (code 1) at io.c(1)\n"
	err := summarizeRunError(errors.New("exit status 1"), raw)
	if strings.Contains(err.Error(), "Alguns arquivos") {
		t.Fatalf("should not claim unreadable files when none were found: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "rsync error: something unrelated") {
		t.Fatalf("expected the fallback to surface rsync's own summary line, got: %s", err.Error())
	}
}
