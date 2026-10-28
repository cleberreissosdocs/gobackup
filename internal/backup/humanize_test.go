package backup

import "testing"

func TestHumanizeProgressLine(t *testing.T) {
	cases := []struct{ in, wantContains string }{
		{"8,062,193,286  2%  136.33MB/s    0:40:48", "2% concluído"},
		{"8,062,193,286  2%  136.33MB/s    0:40:48 (xfr#153, to-chk=1234543/9876543)", "arquivos restantes"},
		{"building file list...", "building file list..."}, // unrecognized -> passthrough
		{"sent 123 bytes  received 45 bytes  1,234.00 bytes/sec", "sent 123 bytes  received 45 bytes  1,234.00 bytes/sec"},
	}
	for _, c := range cases {
		got := humanizeProgressLine(c.in)
		if !contains(got, c.wantContains) {
			t.Errorf("humanizeProgressLine(%q) = %q, want substring %q", c.in, got, c.wantContains)
		}
		t.Logf("%q -> %q", c.in, got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (sub == "" || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
