//go:build !windows

// Package main (netauth_other.go): non-Windows stubs for SMB/UNC
// authentication (see netauth_windows.go). UNCServerRoot is pure string
// handling and is shared verbatim so it can be unit-tested on Linux;
// only the actual WNetAddConnection2W call is Windows-only.
package winagentcore

// ConnectUNC is a no-op for a non-UNC path (so unconditional calls are
// safe) and an explicit failure otherwise: a Linux build cannot
// authenticate to an SMB share this way, and pretending it succeeded
// would turn a clear error into a confusing permission denied later.
func ConnectUNC(uncPath, username, password string) error {
	if UNCServerRoot(uncPath) == "" || username == "" {
		return nil
	}
	return errNetworkCredsWindowsOnly
}

func DisconnectUNC(uncPath string) error { return nil }
