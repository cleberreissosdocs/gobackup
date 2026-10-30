//go:build !windows

// Package main (netcred_other.go): non-Windows stubs for the SMB/UNC
// credential storage implemented with Windows DPAPI in
// netcred_windows.go. This package is deliberately buildable on Linux (see
// mirror.go's package doc) so the copy engine can be unit-tested without a
// Windows box, so these have to exist — but they must FAIL rather than
// degrade.
//
// Returning an error instead of, say, storing the password as-is is the
// whole point: a silent passthrough here would mean a Linux build writing
// plaintext share passwords into jobs.json, which is exactly the outcome
// the DPAPI design exists to prevent. Network folders are a Windows-agent
// feature; on any other platform the honest answer is "not supported".
package winagentcore

import "errors"

var errNetworkCredsWindowsOnly = errors.New("credenciais de pasta de rede só são suportadas no agente Windows")

func EncryptSecret(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	return "", errNetworkCredsWindowsOnly
}

func DecryptSecret(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	return "", errNetworkCredsWindowsOnly
}
