package core

import (
	"strings"
	"testing"
)

func TestTruncateExecutionErrorShort(t *testing.T) {
	msg := "rsync failed: no such file or directory"
	if got := TruncateExecutionError(msg); got != msg {
		t.Errorf("short message was altered: %q", got)
	}
}

func TestTruncateExecutionErrorLong(t *testing.T) {
	// Simulates the real-world case: thousands of \r progress updates
	// glued into one giant token by a \n-only line scanner.
	var b strings.Builder
	for i := 0; i < 200000; i++ {
		b.WriteString("0 0% 0.00kB/s 0:00:00 (xfr#0, ir-chk=1/1)\r")
	}
	huge := b.String()
	got := TruncateExecutionError(huge)
	if len(got) >= len(huge) {
		t.Fatalf("expected truncation, got length %d >= original %d", len(got), len(huge))
	}
	if !strings.Contains(got, "truncada") {
		t.Errorf("truncated message must say it was truncated: %q", got[len(got)-80:])
	}
	if !strings.HasPrefix(got, huge[:100]) {
		t.Errorf("truncation must keep the original prefix intact")
	}
}
