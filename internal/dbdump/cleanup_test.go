package dbdump

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/cleber/gobackup/internal/core"
)

// A failed dump must leave NOTHING behind. This caught a real Windows
// bug: the output file was removed while still open, which POSIX allows
// and Windows refuses, so every failed database backup left a 23-byte
// empty gzip in the destination. From a directory listing that file is
// indistinguishable from a real backup, and retention would later count
// it as one.
//
// The test passes on Linux either way (unlinking an open file works
// there); it is the Windows agent it protects, and it also documents the
// invariant so the explicit close is not "cleaned up" later.
func TestFailedDumpLeavesNoFileBehind(t *testing.T) {
	for _, compression := range []core.Compression{
		core.CompressionNone, core.CompressionGzip, core.CompressionZip,
	} {
		t.Run(string(compression), func(t *testing.T) {
			dir := t.TempDir()

			_, err := Run(context.Background(), Options{
				Engine:   core.DBEngine("motor-que-nao-existe"),
				Database: "qualquer",
			}, dir, compression)
			if err == nil {
				t.Fatal("esperava erro para um motor desconhecido")
			}

			entries, readErr := os.ReadDir(dir)
			if readErr != nil {
				t.Fatalf("ler o destino: %v", readErr)
			}
			if len(entries) != 0 {
				var nomes []string
				for _, e := range entries {
					info, _ := e.Info()
					size := int64(-1)
					if info != nil {
						size = info.Size()
					}
					nomes = append(nomes, e.Name()+" ("+itoa(size)+" bytes)")
				}
				t.Errorf("a falha deixou arquivo no destino: %s", strings.Join(nomes, ", "))
			}
		})
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
