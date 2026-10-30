// Command netcredcheck is a throwaway verification harness for the
// Windows-only SMB credential plumbing (DPAPI protect/unprotect and
// WNetAddConnection2 deviceless authentication). It exists to prove those
// syscalls actually work in the SAME context the agent runs in -- Local
// System, no interactive session -- rather than only in a developer's
// logged-in shell, where DPAPI machine scope and the SMB redirector
// behave differently enough to hide real bugs.
package main

import (
	"fmt"
	"os"

	"github.com/cleber/gobackup/internal/winagentcore"
)

func main() {
	fmt.Println("== contexto ==")
	host, _ := os.Hostname()
	fmt.Printf("host=%s\n", host)

	fmt.Println("\n== DPAPI (escopo de máquina) ==")
	secret := "senha-de-teste-ação-€"
	enc, err := winagentcore.EncryptSecret(secret)
	if err != nil {
		fmt.Println("FALHA ao cifrar:", err)
		os.Exit(1)
	}
	fmt.Printf("cifrado ok, %d bytes em base64\n", len(enc))
	if enc == secret {
		fmt.Println("FALHA: saída idêntica ao texto puro")
		os.Exit(1)
	}
	dec, err := winagentcore.DecryptSecret(enc)
	if err != nil {
		fmt.Println("FALHA ao decifrar:", err)
		os.Exit(1)
	}
	if dec != secret {
		fmt.Printf("FALHA: ida e volta não bate (%q != %q)\n", dec, secret)
		os.Exit(1)
	}
	fmt.Println("ida e volta OK (inclusive acentos/UTF-8)")

	// A corrupted/foreign blob must ERROR, never silently yield "".
	if _, err := winagentcore.DecryptSecret("bm90LWEtcmVhbC1ibG9i"); err == nil {
		fmt.Println("FALHA: blob inválido decifrou sem erro")
		os.Exit(1)
	}
	fmt.Println("blob inválido rejeitado corretamente")

	fmt.Println("\n== UNCServerRoot ==")
	for _, c := range []string{
		`\\192.168.8.250\repositorio\2026`,
		`\\192.168.8.250\repositorio`,
		`C:\dados`,
		`//192.168.8.250/repositorio/2026`,
	} {
		fmt.Printf("%-40q -> %q\n", c, winagentcore.UNCServerRoot(c))
	}

	if len(os.Args) > 3 {
		path, user, pass := os.Args[1], os.Args[2], os.Args[3]
		fmt.Println("\n== ConnectUNC (credenciais reais) ==")
		if err := winagentcore.ConnectUNC(path, user, pass); err != nil {
			fmt.Println("FALHA ao conectar:", err)
			os.Exit(1)
		}
		fmt.Println("autenticado")
		entries, err := os.ReadDir(path)
		if err != nil {
			fmt.Println("FALHA ao listar:", err)
			os.Exit(1)
		}
		fmt.Printf("listagem OK: %d entradas\n", len(entries))
		for i, e := range entries {
			if i >= 5 {
				break
			}
			fmt.Printf("  %v %s\n", e.IsDir(), e.Name())
		}
		_ = winagentcore.DisconnectUNC(path)
	}
	fmt.Println("\nTUDO OK")
}
