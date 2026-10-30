package winagentcore

import (
	"runtime"
	"testing"
)

// A UNC path must survive NormalizeWindowsPath intact. If it comes out
// unrecognizable as UNC, UNCServerRoot returns "" further down, ConnectUNC
// silently becomes a no-op, and the job fails much later with "path not
// found" instead of naming a credentials problem — the exact silent
// failure mode this normalization exists to prevent.
//
// filepath.Clean is volume-aware only on a Windows build, so the
// Clean-dependent expectations run there; the leading-slash fixes are
// pure string handling and are checked everywhere.
func TestNormalizeWindowsPathKeepsUNC(t *testing.T) {
	cases := []struct {
		in, want, why string
	}{
		{`\\srv\share`, `\\srv\share`, "UNC digitado direto"},
		{`\\srv\share\sub`, `\\srv\share\sub`, "UNC com subpasta"},
		{`//srv/share/sub`, `\\srv\share\sub`, "UNC com barras normais"},
		{`/\\srv\share`, `\\srv\share`, "barra solta que o seletor prefixa"},
		{`\\srv\share/sub`, `\\srv\share\sub`, "separadores misturados, como o seletor monta"},
	}
	for _, c := range cases {
		got := NormalizeWindowsPath(c.in)
		if runtime.GOOS != "windows" {
			// Off Windows, filepath.Clean doesn't understand volumes;
			// only assert the part that is pure string handling.
			if UNCServerRoot(got) != `\\srv\share` {
				t.Errorf("NormalizeWindowsPath(%q) = %q, que não é reconhecível como \\\\srv\\share (%s)", c.in, got, c.why)
			}
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeWindowsPath(%q) = %q, want %q (%s)", c.in, got, c.want, c.why)
		}
	}
}

// A local path must keep working exactly as before — the UNC handling is
// additive, not a rewrite of the drive-letter rules.
func TestNormalizeWindowsPathLocalUnaffected(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("filepath.Clean é específico de volume só no build Windows")
	}
	cases := map[string]string{
		`/C:`:            `C:\`,
		`/C:/GoBackup`:   `C:\GoBackup`,
		`C:\dados\sub`:   `C:\dados\sub`,
		`/C:/a/b/..`:     `C:\a`,
		`C:/Program.../`: `C:\Program...`,
	}
	for in, want := range cases {
		if got := NormalizeWindowsPath(in); got != want {
			t.Errorf("NormalizeWindowsPath(%q) = %q, want %q", in, got, want)
		}
	}
}
