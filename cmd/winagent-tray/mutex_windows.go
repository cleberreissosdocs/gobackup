//go:build windows

package main

import (
	"golang.org/x/sys/windows"
)

// singleInstanceGuard prevents launching a second tray app (e.g. the user
// double-clicking the Start Menu shortcut while one is already running
// from the Startup entry) — without this, N copies run simultaneously,
// each fighting over the same config port and job store, which looks
// exactly like "nothing happens when I click the menu" from the outside.
func singleInstanceGuard() (alreadyRunning bool, err error) {
	name, _ := windows.UTF16PtrFromString(`Global\GoBackupWinAgentTray`)
	_, err = windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		return true, nil
	}
	return false, err
}
