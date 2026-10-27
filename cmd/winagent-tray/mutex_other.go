//go:build !windows

package main

// singleInstanceGuard is a no-op off Windows — this binary is only ever
// meant to run there, but the stub keeps `go build ./...` on a Linux dev
// machine compiling cleanly for the whole module.
func singleInstanceGuard() (alreadyRunning bool, err error) {
	return false, nil
}
