// Package main (netcred_windows.go): storage for SMB/UNC share credentials
// used by file jobs whose source or destination is a network folder
// (\\server\share) rather than a local drive.
//
// Why this exists at all: the agent runs as a Windows Service under Local
// System. Drive letters mapped by a logged-in user (Z: -> \\server\share)
// live in that user's logon session (HKCU\Network) and DO NOT exist for a
// service -- which is why such a drive is invisible in the folder picker
// even though C: and D: show up fine. The fix is to address the share by
// its UNC path and authenticate to it explicitly, which needs credentials
// the service can use while nobody is logged in.
//
// How the password is protected: Windows DPAPI with
// CRYPTPROTECT_LOCAL_MACHINE. The ciphertext can only be decrypted on this
// same machine, with no key material for GoBackup to store, invent, or
// leak -- there is no key file to steal alongside the backup config.
//
// What this does NOT protect against, stated plainly because it was a
// deliberate decision and not an oversight: machine scope means ANY
// administrator ON THIS MACHINE can decrypt it, since the agent itself
// must be able to. That is the unavoidable cost of unattended scheduled
// backups -- a credential the service can use without a human present is,
// by definition, one the machine can recover on its own.
package winagentcore

import (
	"encoding/base64"
	"fmt"
	"syscall"
	"unsafe"
)

var (
	crypt32            = syscall.NewLazyDLL("crypt32.dll")
	kernel32Local      = syscall.NewLazyDLL("kernel32.dll")
	procCryptProtect   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotect = crypt32.NewProc("CryptUnprotectData")
	procLocalFree      = kernel32Local.NewProc("LocalFree")
)

// cryptProtectLocalMachine ties the ciphertext to the MACHINE rather than
// to a user profile — required here, since the encrypting context (a UI
// request handled by the service) and the decrypting context (a scheduled
// job run later, possibly after a reboot, with nobody logged in) must both
// succeed with no user session in play.
const cryptProtectLocalMachine = 0x4

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newBlob(b []byte) dataBlob {
	if len(b) == 0 {
		return dataBlob{}
	}
	return dataBlob{cbData: uint32(len(b)), pbData: &b[0]}
}

func (b dataBlob) bytes() []byte {
	if b.cbData == 0 || b.pbData == nil {
		return nil
	}
	out := make([]byte, b.cbData)
	copy(out, unsafe.Slice(b.pbData, b.cbData))
	return out
}

// EncryptSecret protects a plaintext secret with machine-scoped DPAPI and
// returns it base64-encoded, safe to write into the agent's jobs.json.
// An empty input returns an empty string (meaning "no secret stored"),
// never an encrypted empty blob — callers distinguish the two.
func EncryptSecret(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	in := newBlob([]byte(plaintext))
	var out dataBlob
	r, _, err := procCryptProtect.Call(
		uintptr(unsafe.Pointer(&in)),
		0, 0, 0, 0,
		uintptr(cryptProtectLocalMachine),
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return "", fmt.Errorf("CryptProtectData: %w", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return base64.StdEncoding.EncodeToString(out.bytes()), nil
}

// DecryptSecret reverses EncryptSecret. An empty input yields an empty
// secret with no error ("nothing stored" is not a failure). A non-empty
// value that fails to decrypt IS an error: it means the stored credential
// is unusable (typically because jobs.json was copied from another
// machine, which machine-scoped DPAPI deliberately refuses), and the
// caller must surface that rather than silently attempting an
// unauthenticated connection.
func DecryptSecret(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("stored credential is not valid base64: %w", err)
	}
	in := newBlob(raw)
	var out dataBlob
	r, _, callErr := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(&in)),
		0, 0, 0, 0,
		uintptr(cryptProtectLocalMachine),
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return "", fmt.Errorf("CryptUnprotectData (credential may have been copied from another machine): %w", callErr)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return string(out.bytes()), nil
}
