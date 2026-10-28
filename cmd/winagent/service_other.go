//go:build !windows

// Package main (service_other.go): stub for non-Windows builds (this repo
// builds cmd/winagent on Linux dev machines too, just never runs it there)
// — keeps main.go's dispatch table unconditional instead of needing its
// own build tags.
package main

import "fmt"

func cmdServiceInstall() {
	fmt.Println("service-install só é suportado no Windows.")
}

func cmdServiceUninstall() {
	fmt.Println("service-uninstall só é suportado no Windows.")
}

func cmdServiceRun(args []string) {
	fmt.Println("service-run só é suportado no Windows — rodando 'serve' em primeiro plano.")
	cmdServe(args)
}
