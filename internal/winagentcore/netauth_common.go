// Package main (netauth_common.go): the platform-independent string
// handling behind SMB/UNC authentication, kept out of the _windows/_other
// files so there is ONE copy of these rules and so they can be unit-tested
// on any platform. Only the actual WNetAddConnection2W syscall is
// Windows-only.
package winagentcore

import "strings"

// UNCServerRoot reduces any UNC path to the \\server\share prefix that SMB
// authenticates against, since credentials are registered per share, not
// per subdirectory. Returns "" for anything that is not a UNC path (a
// normal local path like C:\dados needs no authentication at all).
func UNCServerRoot(path string) string {
	p := strings.ReplaceAll(strings.TrimSpace(path), "/", `\`)
	if !strings.HasPrefix(p, `\\`) {
		return ""
	}
	// TrimLeft, not TrimPrefix: the prefix may itself be more than two
	// separators, and removing exactly two would leave a leading one that
	// turns into an empty first field below.
	//
	// Collapsing repeated separators matters because Windows itself
	// tolerates \\server\\share, and a path can pick up doubled
	// backslashes on the way here (copy-pasted out of an escaped string,
	// a config file, a command line). Failing to recognize such a path as
	// UNC would silently skip authentication, then surface much later as
	// a confusing "path not found" instead of a credentials problem.
	rest := strings.TrimLeft(p, `\`)
	for strings.Contains(rest, `\\`) {
		rest = strings.ReplaceAll(rest, `\\`, `\`)
	}
	parts := strings.Split(rest, `\`)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return `\\` + parts[0] + `\` + parts[1]
}

// qualifyUser prefixes a bare username with the share's server, turning
// "Administrador" into `192.168.8.250\Administrador`.
//
// This is not cosmetic. Verified on a real workgroup server, running as
// Local System: connecting as the REMOTE machine's local account with an
// unqualified name fails with Windows error 1312
// (ERROR_NO_SUCH_LOGON_SESSION), while the identical call with the
// server-qualified name authenticates and lists the share. The
// unqualified name is ambiguous — the local machine has its own
// "Administrador" too — so the redirector does not resolve it to the
// remote account. Nobody types the qualified form spontaneously, so doing
// it here is the difference between the feature working and the operator
// getting a bare numeric error.
//
// Already-qualified input is left alone: a backslash means DOMAIN\user or
// SERVER\user, and an "@" means a UPN (user@domain) — both chosen
// deliberately by whoever typed them.
func qualifyUser(share, username string) string {
	if strings.ContainsAny(username, `\@`) {
		return username
	}
	server := strings.TrimPrefix(share, `\\`)
	if i := strings.IndexByte(server, '\\'); i >= 0 {
		server = server[:i]
	}
	if server == "" {
		return username
	}
	return server + `\` + username
}
