// Package main (netauth_windows.go): authenticates this process to an
// SMB/UNC server so a file job can read from or write to \\server\share
// while running as a service with nobody logged in.
//
// Uses WNetAddConnection2W WITHOUT a local drive letter (lpLocalName nil)
// — a "deviceless" connection. That matters: mapping an actual letter
// would be both pointless (the letter would exist only inside this
// service's session, which is the very problem we are solving) and racy
// (two jobs picking the same free letter). A deviceless connection simply
// registers credentials for that server with the SMB redirector, after
// which ordinary file I/O on \\server\share just works for this session.
package winagentcore

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	mpr                      = syscall.NewLazyDLL("mpr.dll")
	procWNetAddConnection2   = mpr.NewProc("WNetAddConnection2W")
	procWNetCancelConnection = mpr.NewProc("WNetCancelConnection2W")
)

const (
	resourceTypeDisk = 0x00000001
	// Windows error codes worth naming rather than reporting as a bare
	// number, because each needs a different action from the operator.
	errorSessionCredentialConflict = 1219 // already connected with OTHER credentials
	errorAlreadyAssigned           = 85
	errorBadNetName                = 67
	errorLogonFailure              = 1326
	errorAccessDenied              = 5
	errorInvalidPassword           = 86
	// ERROR_NO_SUCH_LOGON_SESSION. Observed empirically as the code a
	// bad credential actually produces when WNetAddConnection2 is called
	// from a SERVICE (Local System, no interactive session) -- verified
	// on a real Windows Server with a deliberately wrong username. The
	// documented 1326 shows up from an interactive session instead, so
	// mapping only 1326 would have left the most likely real-world
	// failure reported as a bare, unexplained number.
	errorNoSuchLogonSession = 1312
)

type netResource struct {
	Scope       uint32
	Type        uint32
	DisplayType uint32
	Usage       uint32
	LocalName   *uint16
	RemoteName  *uint16
	Comment     *uint16
	Provider    *uint16
}

// ConnectUNC authenticates this process to the share containing uncPath.
// It is a no-op (nil) for a non-UNC path or when no username is supplied,
// so callers can invoke it unconditionally before any file job.
//
// An existing connection to the same share made with DIFFERENT credentials
// is dropped and retried, rather than reported as an error: Windows
// refuses a second set of credentials for one server (ERROR_SESSION_
// CREDENTIAL_CONFLICT, 1219), which would otherwise make a job fail
// permanently just because an earlier job — or an interactive user's
// leftover mapping — got there first with another account.
func ConnectUNC(uncPath, username, password string) error {
	share := UNCServerRoot(uncPath)
	if share == "" || username == "" {
		return nil
	}
	username = qualifyUser(share, username)
	err := addConnection(share, username, password)
	if isErrno(err, errorSessionCredentialConflict) || isErrno(err, errorAlreadyAssigned) {
		_ = DisconnectUNC(uncPath)
		err = addConnection(share, username, password)
	}
	if err != nil {
		return fmt.Errorf("conectar em %s como %s: %w", share, username, err)
	}
	return nil
}

func addConnection(share, username, password string) error {
	remote, err := syscall.UTF16PtrFromString(share)
	if err != nil {
		return err
	}
	user, err := syscall.UTF16PtrFromString(username)
	if err != nil {
		return err
	}
	pass, err := syscall.UTF16PtrFromString(password)
	if err != nil {
		return err
	}
	nr := netResource{
		Type:       resourceTypeDisk,
		LocalName:  nil, // deviceless: no drive letter, see package doc
		RemoteName: remote,
	}
	r, _, _ := procWNetAddConnection2.Call(
		uintptr(unsafe.Pointer(&nr)),
		uintptr(unsafe.Pointer(pass)),
		uintptr(unsafe.Pointer(user)),
		0, // no CONNECT_UPDATE_PROFILE: nothing persisted into a profile
	)
	if r != 0 {
		return explainNetError(uint32(r))
	}
	return nil
}

// DisconnectUNC drops this process's connection to the share containing
// uncPath. Best-effort: failing to disconnect never fails a backup.
func DisconnectUNC(uncPath string) error {
	share := UNCServerRoot(uncPath)
	if share == "" {
		return nil
	}
	remote, err := syscall.UTF16PtrFromString(share)
	if err != nil {
		return err
	}
	r, _, _ := procWNetCancelConnection.Call(uintptr(unsafe.Pointer(remote)), 0, 1)
	if r != 0 {
		return explainNetError(uint32(r))
	}
	return nil
}

type netErrno uint32

func (e netErrno) Error() string { return fmt.Sprintf("erro do Windows %d", uint32(e)) }

func isErrno(err error, code uint32) bool {
	var e netErrno
	if err == nil {
		return false
	}
	if ne, ok := err.(netErrno); ok {
		e = ne
		return uint32(e) == code
	}
	return false
}

// explainNetError turns the handful of codes an operator actually hits
// into a sentence naming the fix, instead of a number they would have to
// look up. Anything unrecognized keeps the raw code (never swallowed).
func explainNetError(code uint32) error {
	switch code {
	case 0:
		return nil
	case errorLogonFailure, errorAccessDenied, errorInvalidPassword, errorNoSuchLogonSession:
		return fmt.Errorf("usuário ou senha do compartilhamento incorretos, ou o servidor recusou a sessão (erro %d)", code)
	case errorBadNetName:
		return fmt.Errorf("compartilhamento não encontrado no servidor (erro %d)", code)
	default:
		return netErrno(code)
	}
}
